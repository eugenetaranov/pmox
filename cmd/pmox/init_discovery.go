package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func discoveryCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

// pickOneAuto returns the sole option, reporting it, when exactly one
// exists; otherwise it defers to the interactive picker. This keeps
// configure from prompting for a choice that has only one answer.
func pickOneAuto(p prompter, title string, opts []huh.Option[string], fallback string) string {
	if len(opts) == 1 {
		p.Printf("%s: %s\n", title, opts[0].Key)
		return opts[0].Value
	}
	return tui.SelectOne(title, opts, fallback)
}

func pickNode(ctx context.Context, p prompter, client *pveclient.Client) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	nodes, err := client.ListNodes(dctx)
	if err != nil {
		p.Errf("could not list nodes: %v\n", err)
		ans, _ := p.Prompt("Default node: ")
		return strings.TrimSpace(ans)
	}
	if len(nodes) == 0 {
		ans, _ := p.Prompt("Default node: ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(nodes))
	for _, n := range nodes {
		label := n.Node
		if n.Status != "" {
			label = fmt.Sprintf("%s (%s)", n.Node, n.Status)
		}
		opts = append(opts, huh.NewOption(label, n.Node))
	}
	return pickOneAuto(p, "Default node", opts, nodes[0].Node)
}

func pickTemplate(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	tmpls, total, err := client.ListTemplates(dctx, node)
	if err != nil {
		p.Errf("could not list templates on node %s: %v\n", node, err)
		ans, _ := p.Prompt("Default template (VMID): ")
		return strings.TrimSpace(ans)
	}
	if len(tmpls) == 0 {
		if total == 0 {
			p.Errf("no VMs visible on node %s — the API token cannot see any VMs.\n", node)
			p.Errf("  Fix: grant VM.Audit on /vms to the token's user, OR\n")
			p.Errf("       edit the token in Datacenter → Permissions → API Tokens\n")
			p.Errf("       and uncheck 'Privilege Separation' so it inherits the user's rights.\n")
			p.Errf("  See README.md → 'Required permissions' for the full list.\n")
		} else {
			p.Errf("node %s has %d VMs but none are marked as templates\n", node, total)
			p.Errf("  Fix: in the PVE web UI, right-click a VM → Convert to template.\n")
		}
		ans, _ := p.Prompt("Default template (VMID): ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(tmpls))
	for _, t := range tmpls {
		label := fmt.Sprintf("%d  %s", t.VMID, t.Name)
		opts = append(opts, huh.NewOption(label, strconv.Itoa(t.VMID)))
	}
	return pickOneAuto(p, "Default template", opts, strconv.Itoa(tmpls[0].VMID))
}

func pickStorage(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	if err != nil {
		p.Errf("could not list storage on node %s: %v\n", node, err)
		p.Errf("  (the API token likely needs Datastore.Audit on /storage)\n")
		ans, _ := p.Prompt("Default storage: ")
		return strings.TrimSpace(ans)
	}
	if len(pools) == 0 {
		p.Errf("no storage pools returned for node %s\n", node)
		ans, _ := p.Prompt("Default storage: ")
		return strings.TrimSpace(ans)
	}
	// Filter to storages that can actually hold VM disk images.
	usable := pveclient.FilterStorage(pools, pveclient.Storage.SupportsVMDisks)
	if len(usable) == 0 {
		usable = pools
	}
	opts := make([]huh.Option[string], 0, len(usable))
	for _, s := range usable {
		label := fmt.Sprintf("%s (%s)", s.Storage, s.Type)
		opts = append(opts, huh.NewOption(label, s.Storage))
	}
	return pickOneAuto(p, "Default storage", opts, usable[0].Storage)
}

// snippetCapableTypes lists the PVE storage backends that can host the
// `snippets` content type. dir is the common case; nfs/cifs/cephfs are
// the other directory-shaped backends PVE accepts snippets on.
var snippetCapableTypes = map[string]bool{
	"dir": true, "nfs": true, "cifs": true, "cephfs": true,
}

// snippetStoragePicker abstracts the storage list and update calls
// pickSnippetStorage needs. Tests stub this with an in-memory fake to
// avoid spinning up an httptest server just for two endpoints.
type snippetStoragePicker interface {
	ListStorage(ctx context.Context, node string) ([]pveclient.Storage, error)
	UpdateStorageContent(ctx context.Context, storage string, content []string) error
}

// selectSnippetStorageFn is a test seam over tui.SelectOne so the
// multi-match branch can be driven without a real terminal.
var selectSnippetStorageFn = func(title string, opts []huh.Option[string], fallback string) string {
	return tui.SelectOne(title, opts, fallback)
}

// pickSnippetStorage resolves the storage that pmox will use for
// cloud-init snippets. Decision tree: exactly one snippet-capable
// storage → silent; multiple → TUI picker; zero → offer to enable
// snippets on an existing dir-backed storage.
func pickSnippetStorage(ctx context.Context, p prompter, client snippetStoragePicker, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	if err != nil {
		p.Errf("could not list storage on node %s: %v\n", node, err)
		return ""
	}

	matches := pveclient.FilterStorage(pools, pveclient.Storage.SupportsSnippets)
	switch len(matches) {
	case 1:
		p.Printf("Snippet storage: %s\n", matches[0].Storage)
		return matches[0].Storage
	case 0:
		return offerEnableSnippets(ctx, p, client, pools)
	}
	opts := make([]huh.Option[string], 0, len(matches))
	for _, s := range matches {
		label := fmt.Sprintf("%s (%s)", s.Storage, s.Type)
		opts = append(opts, huh.NewOption(label, s.Storage))
	}
	return selectSnippetStorageFn("Snippet storage", opts, matches[0].Storage)
}

// offerEnableSnippets is the zero-match branch of pickSnippetStorage.
// It looks for a dir-backed storage to enable `snippets` on, defaults
// to "local" when present, and on confirmation issues
// UpdateStorageContent. Decline or absence prints the manual remediation
// and returns "" so credentials still save.
func offerEnableSnippets(ctx context.Context, p prompter, client snippetStoragePicker, pools []pveclient.Storage) string {
	capable := pveclient.FilterStorage(pools, func(s pveclient.Storage) bool { return snippetCapableTypes[s.Type] })
	if len(capable) == 0 {
		printSnippetManualRemediation(p)
		return ""
	}
	target := capable[0]
	for _, s := range capable {
		if s.Storage == "local" {
			target = s
			break
		}
	}
	ans, err := p.Prompt(fmt.Sprintf("no storage supports snippets. enable snippets on %q? [Y/n]: ", target.Storage))
	if err != nil {
		return ""
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans != "" && ans != "y" && ans != "yes" {
		printSnippetManualRemediation(p)
		return ""
	}

	newContent := target.ContentList()
	if !slices.Contains(newContent, "snippets") {
		newContent = append(newContent, "snippets")
	}
	if err := client.UpdateStorageContent(ctx, target.Storage, newContent); err != nil {
		p.Errf("could not enable snippets on %q: %v\n", target.Storage, err)
		printSnippetManualRemediation(p)
		return ""
	}
	p.Printf("enabled snippets on %s\n", target.Storage)
	return target.Storage
}

func printSnippetManualRemediation(p prompter) {
	p.Errf("no snippet storage configured.\n")
	p.Errf("  Fix: edit /etc/pve/storage.cfg on the PVE host and add 'snippets' to\n")
	p.Errf("       the content= line of a directory-backed storage, then re-run\n")
	p.Errf("       'pmox init' to record it.\n")
}

func pickBridge(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	bridges, err := client.ListBridges(dctx, node)
	if err != nil {
		p.Errf("could not list bridges on node %s: %v\n", node, err)
		p.Errf("  (the API token likely needs SDN.Audit or Sys.Audit on /nodes/%s)\n", node)
		ans, _ := p.Prompt("Default bridge: ")
		return strings.TrimSpace(ans)
	}
	if len(bridges) == 0 {
		p.Errf("no bridges returned for node %s\n", node)
		ans, _ := p.Prompt("Default bridge: ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(bridges))
	for _, b := range bridges {
		opts = append(opts, huh.NewOption(b.Iface, b.Iface))
	}
	return pickOneAuto(p, "Default bridge", opts, bridges[0].Iface)
}
