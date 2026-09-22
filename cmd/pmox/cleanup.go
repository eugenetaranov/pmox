package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// snippetFileRe matches a pmox-owned cloud-init snippet and captures its
// VMID, whether given as a bare filename or a full PVE volid.
var snippetFileRe = regexp.MustCompile(`(?:^|/)pmox-(\d+)-user-data\.yaml$`)

// cleanupItem is one removable leftover. apply performs the removal.
type cleanupItem struct {
	Category string `json:"category"`
	Detail   string `json:"detail"`
	apply    func() error
}

func newCleanupCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove pmox leftovers: orphaned snippets and stale local state",
		Long: `Reclaim cruft pmox can leave behind:

  - cloud-init snippets on the cluster whose VM no longer exists
    (e.g. a VM deleted via the web UI, or an interrupted 'pmox delete')
  - dead mount records and orphaned mount logs in the local state dir
  - guest known_hosts pins for IPs that no longer belong to a pmox VM

It scans every configured context. Dry-run by default — it only reports
what it would remove; pass --apply to actually delete. Only pmox-owned
resources are ever touched; VMs are never removed (use 'pmox delete').`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCleanup(cmd, apply) },
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually remove the items (default: dry-run report)")
	return cmd
}

func runCleanup(cmd *cobra.Command, apply bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ew := cmd.ErrOrStderr()

	var items []cleanupItem
	liveIPs := map[string]bool{}
	ipsComplete := true // false if we can't enumerate every live pmox VM IP

	// --- Remote: per-context snippet orphans + live pmox VM IPs ---
	for _, url := range cfg.ServerURLs() {
		srv := cfg.Servers[url]
		label := contextLabelFor(cfg, url)
		client, cerr := cleanupClient(url, srv)
		if cerr != nil {
			fmt.Fprintf(ew, "cleanup: skipping context %s: %v\n", label, cerr)
			ipsComplete = false
			continue
		}
		resources, rerr := client.ClusterResources(ctx, "vm")
		if rerr != nil {
			fmt.Fprintf(ew, "cleanup: %s: could not list VMs: %v\n", label, rerr)
			ipsComplete = false
			continue
		}
		vmids := make(map[int]bool, len(resources))
		for _, r := range resources {
			vmids[r.VMID] = true
		}
		for _, r := range resources {
			if !vm.HasPMOXTag(r.Tags) || r.Status != "running" {
				continue
			}
			ifaces, aerr := client.AgentNetwork(ctx, r.Node, r.VMID)
			if aerr != nil {
				ipsComplete = false // can't confirm this VM's IP → don't risk pruning its pin
				continue
			}
			if ip := launch.PickIPv4(ifaces); ip != "" {
				liveIPs[ip] = true
			}
		}

		if srv.Node == "" {
			continue // no node → can't scope snippet storage
		}
		for _, storage := range snippetStoragesFor(srv) {
			contents, serr := client.ListStorageContent(ctx, srv.Node, storage, "snippets")
			if serr != nil {
				continue
			}
			for _, item := range contents {
				m := snippetFileRe.FindStringSubmatch(item.Volid)
				if m == nil {
					continue
				}
				vmid, _ := strconv.Atoi(m[1])
				if vmids[vmid] {
					continue // the VM still exists — keep its snippet
				}
				filename := item.Volid[strings.LastIndex(item.Volid, "/")+1:]
				c, node, st, fn := client, srv.Node, storage, filename
				items = append(items, cleanupItem{
					Category: "snippet",
					Detail:   fmt.Sprintf("%s: %s on %s (vmid %d no longer exists)", label, filename, storage, vmid),
					apply:    func() error { return c.DeleteSnippet(ctx, node, st, fn) },
				})
			}
		}
	}

	// --- Local: mount records + orphaned logs ---
	items = append(items, localMountItems()...)

	// --- Local: stale guest known_hosts pins ---
	items = append(items, staleKnownHostItems(liveIPs, ipsComplete, ew)...)

	return reportCleanup(cmd, items, apply)
}

// cleanupClient builds a PVE client for a configured server, pulling its
// secret from the keychain. Used to scan every context, not just the
// resolved one.
func cleanupClient(url string, srv *config.Server) (*pveclient.Client, error) {
	secret, err := credstore.Get(url)
	if err != nil {
		return nil, err
	}
	return pveclient.New(url, srv.TokenID, secret, srv.Insecure), nil
}

// contextLabelFor returns the context name for a URL (for readable output).
func contextLabelFor(cfg *config.Config, url string) string {
	for _, c := range cfg.Contexts() {
		if c.URL == url {
			return c.Name
		}
	}
	return url
}

// snippetStoragesFor returns the storages that might hold this server's
// pmox snippets (snippet_storage, and the disk storage as a fallback).
func snippetStoragesFor(srv *config.Server) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []string{srv.SnippetStorage, srv.Storage} {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// localMountItems finds dead mount records (and their logs) plus orphaned
// log files under the mount state dir.
func localMountItems() []cleanupItem {
	stateDir := mountStateDir()
	records, _ := mount.List(stateDir)
	// Every log that belongs to a record (live or dead) is handled via its
	// record, so it must not also be flagged as an orphan.
	recordLogs := map[string]bool{}
	var items []cleanupItem
	for _, rec := range records {
		if rec.LogPath != "" {
			recordLogs[rec.LogPath] = true
		}
		if mount.Alive(rec.PID) && !mount.LooksReused(rec.PID) {
			continue
		}
		r := rec
		items = append(items, cleanupItem{
			Category: "mount-record",
			Detail:   fmt.Sprintf("%s → %s:%s (pid %d is not a live mount)", r.LocalPath, r.VMName, r.RemotePath, r.PID),
			apply: func() error {
				_ = mount.Remove(r)
				if r.LogPath != "" {
					_ = os.Remove(r.LogPath)
				}
				return nil
			},
		})
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return items
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		p := filepath.Join(stateDir, e.Name())
		if recordLogs[p] {
			continue // handled by its record
		}
		pp := p
		items = append(items, cleanupItem{
			Category: "log",
			Detail:   "orphaned log " + p,
			apply:    func() error { return os.Remove(pp) },
		})
	}
	return items
}

// staleKnownHostItems returns a single item that prunes guest known_hosts
// pins whose host is not a current pmox VM IP. It refuses to act unless
// the live-IP set is complete, so a transient API/agent failure can't
// cause a valid pin to be pruned.
func staleKnownHostItems(liveIPs map[string]bool, complete bool, ew io.Writer) []cleanupItem {
	path, err := guestKnownHostsPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // missing file → nothing to prune
	}
	if !complete {
		fmt.Fprintln(ew, "cleanup: skipping known_hosts pruning (couldn't enumerate every live VM IP this run)")
		return nil
	}
	var staleHosts []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		host := knownHostToken(trimmed)
		if host != "" && !liveIPs[host] {
			staleHosts = append(staleHosts, host)
		}
	}
	if len(staleHosts) == 0 {
		return nil
	}
	return []cleanupItem{{
		Category: "known-host",
		Detail:   fmt.Sprintf("%d stale pin(s): %s", len(staleHosts), strings.Join(staleHosts, ", ")),
		apply:    func() error { return pruneKnownHosts(path, liveIPs) },
	}}
}

// knownHostToken returns the host field of a known_hosts line, stripping
// any [host]:port bracketing and a trailing :port.
func knownHostToken(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	h := fields[0]
	// Take the first host if it's a comma-list, and strip [..]:port form.
	if i := strings.IndexByte(h, ','); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.ReplaceAll(h, "]", "")
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i+1:], ":") {
		// strip a trailing :port (but not part of an IPv6 literal)
		if _, err := strconv.Atoi(h[i+1:]); err == nil {
			h = h[:i]
		}
	}
	return h
}

// pruneKnownHosts rewrites the known_hosts file, dropping lines whose host
// is not in liveIPs.
func pruneKnownHosts(path string, liveIPs map[string]bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
			continue
		}
		if host := knownHostToken(trimmed); host != "" && !liveIPs[host] {
			continue // stale — drop
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// reportCleanup prints (and, with apply, performs) the planned removals.
func reportCleanup(cmd *cobra.Command, items []cleanupItem, apply bool) error {
	w := cmd.OutOrStdout()

	if outputMode == "json" {
		out := struct {
			Applied bool          `json:"applied"`
			Items   []cleanupItem `json:"items"`
			Total   int           `json:"total"`
		}{Applied: apply, Items: items, Total: len(items)}
		if apply {
			for _, it := range items {
				_ = it.apply()
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(items) == 0 {
		fmt.Fprintln(w, "Nothing to clean up.")
		return nil
	}

	// Group by category in a stable order.
	order := []string{"snippet", "mount-record", "log", "known-host"}
	title := map[string]string{
		"snippet":      "Orphaned snippets",
		"mount-record": "Dead mount records",
		"log":          "Orphaned logs",
		"known-host":   "Stale known_hosts pins",
	}
	byCat := map[string][]cleanupItem{}
	for _, it := range items {
		byCat[it.Category] = append(byCat[it.Category], it)
	}
	for _, cat := range order {
		list := byCat[cat]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%d):\n", title[cat], len(list))
		for _, it := range list {
			fmt.Fprintf(w, "  - %s\n", it.Detail)
		}
	}

	if !apply {
		fmt.Fprintf(w, "\nDry run — nothing removed. Re-run with --apply to remove %d item(s).\n", len(items))
		return nil
	}

	var failed int
	for _, it := range items {
		if err := it.apply(); err != nil {
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "failed to remove %q: %v\n", it.Detail, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("removed %d of %d item(s); %d failed", len(items)-failed, len(items), failed)
	}
	fmt.Fprintf(w, "\nRemoved %d item(s).\n", len(items))
	return nil
}
