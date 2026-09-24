package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tackprofile"
	"github.com/eugenetaranov/pmox/internal/tui"
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
	var (
		apply            bool
		only             []string
		skip             []string
		includeTemplates bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove pmox leftovers: orphaned snippets, local state, and templates",
		Long: `Reclaim cruft pmox can leave behind, in selectable categories:

  snippet       cloud-init snippets on the cluster whose VM is gone
  mount-record  dead mount records in the local state dir
  log           orphaned mount logs
  cloud-init    ~/.config/pmox/cloud-init/<slug>.yaml for removed servers
  tack-profile  remembered tack profiles for servers/VMs that are gone
  secret        file-backend secrets.yaml entries for removed servers
  known-host    guest known_hosts pins for IPs no longer on a pmox VM
  ssh-key       the pmox-generated bootstrap SSH key, if no server uses it
  template      pmox-generated templates (DESTRUCTIVE — deletes VMs)

Dry-run by default; pass --apply to delete. On a terminal it shows a
checklist to pick categories (non-destructive ones pre-checked, template
unchecked). Non-interactively, use --only / --skip; add --include-templates
to enable the destructive template category. VMs other than pmox templates
are never removed (use 'pmox delete').

Note: orphaned OS-keychain secrets cannot be enumerated by the OS and so
are not covered here; they are cleared at removal time by
'pmox init --remove' / 'pmox config delete-context'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCleanup(cmd, cleanupOpts{apply: apply, only: only, skip: skip, includeTemplates: includeTemplates})
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually remove the items (default: dry-run report)")
	cmd.Flags().StringSliceVar(&only, "only", nil, "only these categories (comma-separated)")
	cmd.Flags().StringSliceVar(&skip, "skip", nil, "skip these categories (comma-separated)")
	cmd.Flags().BoolVar(&includeTemplates, "include-templates", false, "include the destructive 'template' category (deletes pmox templates)")
	return cmd
}

type cleanupOpts struct {
	apply            bool
	only             []string
	skip             []string
	includeTemplates bool
}

// cleanupCategory describes a removable-leftover category.
type cleanupCategory struct {
	key         string
	title       string
	destructive bool
}

// cleanupCategories is the display/order list of all categories.
var cleanupCategories = []cleanupCategory{
	{"snippet", "Orphaned snippets", false},
	{"template", "pmox templates (DESTRUCTIVE)", true},
	{"mount-record", "Dead mount records", false},
	{"log", "Orphaned logs", false},
	{"cloud-init", "Orphaned cloud-init files", false},
	{"tack-profile", "Stale tack profiles", false},
	{"secret", "Orphaned secrets", false},
	{"known-host", "Stale known_hosts pins", false},
	{"ssh-key", "Orphaned pmox SSH bootstrap key", false},
}

func categoryByKey(k string) (cleanupCategory, bool) {
	for _, c := range cleanupCategories {
		if c.key == k {
			return c, true
		}
	}
	return cleanupCategory{}, false
}

// selectCategoriesFn is a seam over the interactive checklist so tests can
// drive selection without a TTY.
var selectCategoriesFn = tui.SelectMultiChecked

// resolveSelection computes which categories to act on. Precedence:
// --only (exact) wins; otherwise the default safe (non-destructive) set,
// minus --skip, plus template when --include-templates; an interactive
// checklist (when no selection flags and on a TTY) overrides the set.
func resolveSelection(available []string, o cleanupOpts, interactive bool) (map[string]bool, error) {
	for _, k := range append(append([]string{}, o.only...), o.skip...) {
		if _, ok := categoryByKey(k); !ok {
			return nil, fmt.Errorf("%w: unknown cleanup category %q", exitcode.ErrUserInput, k)
		}
	}
	avail := map[string]bool{}
	for _, k := range available {
		avail[k] = true
	}

	sel := map[string]bool{}
	if len(o.only) > 0 {
		for _, k := range o.only {
			if avail[k] {
				sel[k] = true
			}
		}
		return sel, nil
	}
	for _, c := range cleanupCategories {
		if !c.destructive && avail[c.key] {
			sel[c.key] = true
		}
	}
	for _, k := range o.skip {
		delete(sel, k)
	}
	if o.includeTemplates && avail["template"] {
		sel["template"] = true
	}

	hasFlags := len(o.skip) > 0 || o.includeTemplates
	if interactive && !hasFlags {
		opts := make([]huh.Option[string], 0, len(available))
		for _, c := range cleanupCategories {
			if !avail[c.key] {
				continue
			}
			opts = append(opts, huh.NewOption(c.title, c.key).Selected(sel[c.key]))
		}
		chosen, err := selectCategoriesFn("Select what to clean", opts)
		if err != nil {
			return nil, err
		}
		sel = map[string]bool{}
		for _, k := range chosen {
			sel[k] = true
		}
	}
	return sel, nil
}

func runCleanup(cmd *cobra.Command, o cleanupOpts) error {
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
	vmidsByURL := map[string]map[int]bool{}
	reachableURLs := map[string]bool{}

	// --- Remote: per-context snippet orphans, templates, live pmox VM IPs ---
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
		reachableURLs[url] = true
		vmids := make(map[int]bool, len(resources))
		for _, r := range resources {
			vmids[r.VMID] = true
		}
		vmidsByURL[url] = vmids
		for _, r := range resources {
			// pmox-generated templates → destructive template category.
			if r.Template == 1 && isPMOXTemplate(r.Name, r.VMID) {
				c, node, vmid, name := client, r.Node, r.VMID, r.Name
				items = append(items, cleanupItem{
					Category: "template",
					Detail:   fmt.Sprintf("%s: %s (vmid %d) on node %s", label, name, vmid, node),
					apply:    func() error { return deleteTemplate(ctx, c, node, vmid) },
				})
			}
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

	// --- Local categories ---
	items = append(items, localMountItems()...)
	items = append(items, staleKnownHostItems(liveIPs, ipsComplete, ew)...)
	items = append(items, cloudInitItems(cfg)...)
	items = append(items, tackProfileItems(cfg, vmidsByURL, reachableURLs)...)
	items = append(items, secretItems(cfg)...)
	items = append(items, sshKeyItems(cfg)...)

	// --- Select which categories to act on ---
	available := presentCategories(items)
	interactive := tui.Interactive() && outputMode != "json"
	selected, err := resolveSelection(available, o, interactive)
	if err != nil {
		return err
	}
	kept := items[:0:0]
	for _, it := range items {
		if selected[it.Category] {
			kept = append(kept, it)
		}
	}

	return reportCleanup(cmd, kept, o.apply)
}

// presentCategories returns the distinct categories that have ≥1 item, in
// the canonical category order.
func presentCategories(items []cleanupItem) []string {
	have := map[string]bool{}
	for _, it := range items {
		have[it.Category] = true
	}
	var out []string
	for _, c := range cleanupCategories {
		if have[c.key] {
			out = append(out, c.key)
		}
	}
	return out
}

// isPMOXTemplate reports whether a template VM was created by pmox: the
// create-template naming convention (contains "-pmox-") within the
// 9000–9099 VMID range. Conservative so a user's own template in that
// range is not matched.
func isPMOXTemplate(name string, vmid int) bool {
	return strings.Contains(name, "-pmox-") && vmid >= 9000 && vmid <= 9099
}

// deleteTemplate destroys a template VM and waits for the task.
func deleteTemplate(ctx context.Context, c *pveclient.Client, node string, vmid int) error {
	upid, err := c.Delete(ctx, node, vmid)
	if err != nil {
		return err
	}
	return c.WaitTask(ctx, node, upid, 120*time.Second)
}

// cloudInitItems flags per-server cloud-init files whose server is no
// longer in the config.
func cloudInitItems(cfg *config.Config) []cleanupItem {
	dir, err := config.CloudInitDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	configured := map[string]bool{}
	for _, url := range cfg.ServerURLs() {
		if p, err := config.CloudInitPath(url); err == nil {
			configured[filepath.Base(p)] = true
		}
	}
	var items []cleanupItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		if configured[e.Name()] {
			continue
		}
		p := filepath.Join(dir, e.Name())
		items = append(items, cleanupItem{
			Category: "cloud-init",
			Detail:   "orphaned cloud-init file " + p,
			apply:    func() error { return os.Remove(p) },
		})
	}
	return items
}

// tackProfileItems flags remembered tack profiles whose server is gone, or
// whose VMID no longer exists on a reachable server. Entries for servers
// that could not be listed this run are left alone.
func tackProfileItems(cfg *config.Config, vmidsByURL map[string]map[int]bool, reachableURLs map[string]bool) []cleanupItem {
	entries, err := tackprofile.All(tackStateDir())
	if err != nil {
		return nil
	}
	var items []cleanupItem
	for _, e := range entries {
		_, configured := cfg.Servers[e.ServerURL]
		stale := false
		switch {
		case !configured:
			stale = true
		case reachableURLs[e.ServerURL]:
			if vmids := vmidsByURL[e.ServerURL]; vmids != nil && !vmids[e.VMID] {
				stale = true
			}
		}
		if !stale {
			continue
		}
		ee := e
		items = append(items, cleanupItem{
			Category: "tack-profile",
			Detail:   fmt.Sprintf("%s vmid %d → profile %q", contextLabelFor(cfg, ee.ServerURL), ee.VMID, ee.Profile),
			apply:    func() error { return tackprofile.Delete(tackStateDir(), ee.ServerURL, ee.VMID) },
		})
	}
	return items
}

// secretItems flags file-backend secrets.yaml entries for servers no
// longer in the config. Keychain entries are not enumerable and are out of
// scope (handled at removal time).
func secretItems(cfg *config.Config) []cleanupItem {
	if credstore.ActiveBackend() != "file" {
		return nil
	}
	urls, err := credstore.FileStoreURLs()
	if err != nil {
		return nil
	}
	var items []cleanupItem
	for _, u := range urls {
		if _, ok := cfg.Servers[u]; ok {
			continue
		}
		uu := u
		items = append(items, cleanupItem{
			Category: "secret",
			Detail:   "orphaned secret for " + uu,
			apply: func() error {
				for _, rmErr := range []error{
					credstore.Remove(uu),
					credstore.RemoveNodeSSHPassword(uu),
					credstore.RemoveNodeSSHKeyPassphrase(uu),
				} {
					if rmErr != nil && !errors.Is(rmErr, credstore.ErrNotFound) {
						return rmErr
					}
				}
				return nil
			},
		})
	}
	return items
}

// sshKeyItems flags the pmox-generated bootstrap SSH key
// (~/.ssh/pmox_ed25519[.pub], see generateBootstrapKey) when no configured
// server references it. Only this fixed, pmox-owned path is ever
// considered — a key the user pointed pmox at via "use existing" or
// "browse" is never a cleanup candidate.
func sshKeyItems(cfg *config.Config) []cleanupItem {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	priv := filepath.Join(home, ".ssh", "pmox_ed25519")
	pub := priv + ".pub"
	if _, err := os.Stat(pub); err != nil {
		return nil
	}
	for _, srv := range cfg.Servers {
		if srv.SSHPubkey == pub {
			return nil // still in use
		}
	}
	return []cleanupItem{{
		Category: "ssh-key",
		Detail:   "orphaned pmox bootstrap SSH key " + pub + " (and its private key)",
		apply: func() error {
			if err := os.Remove(priv); err != nil && !os.IsNotExist(err) {
				return err
			}
			return os.Remove(pub)
		},
	}}
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

	// Group by category in the canonical order (from cleanupCategories).
	byCat := map[string][]cleanupItem{}
	for _, it := range items {
		byCat[it.Category] = append(byCat[it.Category], it)
	}
	for _, c := range cleanupCategories {
		list := byCat[c.key]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%d):\n", c.title, len(list))
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
