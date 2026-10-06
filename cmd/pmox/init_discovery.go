package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func discoveryCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

// discoverDefaults runs the node/template/storage/snippet/bridge pickers
// in order. current seeds each picker's highlighted option with the
// value from a previous pass through this stage — either earlier in
// this same wizard run, or (for 'pmox config edit') what's already
// configured on disk — so revisiting a choice starts from what's
// already set instead of always the first listed option. A zero-value
// current picks exactly as before: no option pre-highlighted beyond
// the picker's own listed order.
//
// A picker abort (tui.ErrAborted) or a context cancellation (e.g.
// Ctrl-C at a fallback text prompt) stops the sequence and is reported
// as a user-input error.
func discoverDefaults(ctx context.Context, p prompter, client *pveclient.Client, current defaultsAnswers) (defaultsAnswers, error) {
	var d defaultsAnswers
	steps := []struct {
		dst  *string
		pick func() (string, error)
	}{
		{&d.node, func() (string, error) { return pickNode(ctx, p, client, current.node) }},
		{&d.template, func() (string, error) { return pickTemplate(ctx, p, client, d.node, current.template) }},
		{&d.storage, func() (string, error) { return pickStorage(ctx, p, client, d.node, current.storage) }},
		{&d.snippetStorage, func() (string, error) { return pickSnippetStorage(ctx, p, client, d.node, current.snippetStorage) }},
		{&d.bridge, func() (string, error) { return pickBridge(ctx, p, client, d.node, current.bridge) }},
	}
	for _, s := range steps {
		v, err := s.pick()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return defaultsAnswers{}, fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
		}
		*s.dst = v
	}
	return d, nil
}

// pickOneAuto returns the sole option, reporting it, when exactly one
// exists; otherwise it defers to the interactive picker. This keeps
// configure from prompting for a choice that has only one answer.
func pickOneAuto(p prompter, title string, opts []huh.Option[string], fallback string) (string, error) {
	if len(opts) == 1 {
		p.Printf("%s: %s\n", title, opts[0].Key)
		return opts[0].Value, nil
	}
	return tui.SelectOne(title, opts, fallback)
}

func pickNode(ctx context.Context, p prompter, client *pveclient.Client, current string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	nodes, err := client.ListNodes(dctx)
	return pickFromChoice(p, nodeChoice(nodes, err, current))
}

// fieldChoice is the pure outcome of one discovery listing: the options
// to offer (and which to preselect), or — when listing failed or came
// back empty — a request for manual entry. notices explain failures and
// are shown before the manual prompt / picker. Shared by the linear
// pick* helpers and the interactive wizard's Defaults page.
type fieldChoice struct {
	title       string // picker title, e.g. "Default node"
	manualLabel string // free-text label when manual, e.g. "Default template (VMID)"
	opts        []huh.Option[string]
	initial     string
	manual      bool
	notices     []notice
}

// pickFromChoice is the linear presentation of a fieldChoice: print its
// notices, then either prompt for free text or run the (auto-selecting)
// picker.
func pickFromChoice(p prompter, c fieldChoice) (string, error) {
	printNotices(p, c.notices)
	if c.manual {
		ans, _ := p.Prompt(c.manualLabel + ": ")
		return strings.TrimSpace(ans), nil
	}
	return pickOneAuto(p, c.title, c.opts, c.initial)
}

func nodeChoice(nodes []pveclient.Node, err error, current string) fieldChoice {
	c := fieldChoice{title: "Default node", manualLabel: "Default node"}
	if err != nil {
		c.manual = true
		c.notices = []notice{warnNotice(fmt.Sprintf("could not list nodes: %v", err))}
		return c
	}
	if len(nodes) == 0 {
		c.manual = true
		return c
	}
	for _, n := range nodes {
		label := n.Node
		if n.Status != "" {
			label = fmt.Sprintf("%s (%s)", n.Node, n.Status)
		}
		c.opts = append(c.opts, huh.NewOption(label, n.Node))
	}
	c.initial = firstNonEmpty(current, nodes[0].Node)
	return c
}

// createTemplateSentinel is pickTemplate's value for "build a new
// template now" instead of picking an existing one — mirrors
// tackDefaultSentinel's convention of an internal value real user
// input can never collide with. persistServer checks for it after
// saving the server (the earliest point node SSH credentials, needed
// for the build's snippet upload, are available) and runs the full
// create-template build in its place.
const createTemplateSentinel = "\x00pmox-create-template"

func pickTemplate(ctx context.Context, p prompter, client *pveclient.Client, node, current string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	tmpls, total, err := client.ListTemplates(dctx, node)
	// Offering to build one on the spot needs a real terminal — the
	// build has its own pickers (image, target/snippets storage) and
	// runs for several minutes. Non-interactively this must stay byte-
	// for-byte the old behavior: pick among existing templates, or the
	// manual-VMID prompt when none exist.
	return pickFromChoice(p, templateChoice(tmpls, total, err, node, current, tui.Interactive()))
}

func templateChoice(tmpls []pveclient.Template, total int, err error, node, current string, offerBuild bool) fieldChoice {
	c := fieldChoice{title: "Default template", manualLabel: "Default template (VMID)"}
	if err != nil {
		c.manual = true
		c.notices = []notice{warnNotice(fmt.Sprintf("could not list templates on node %s: %v", node, err))}
		return c
	}

	if len(tmpls) == 0 {
		if total == 0 {
			c.notices = []notice{
				warnNotice(fmt.Sprintf("no VMs visible on node %s — the API token cannot see any VMs.", node)),
				warnNotice("  Fix: grant VM.Audit on /vms to the token's user, OR"),
				warnNotice("       edit the token in Datacenter → Permissions → API Tokens"),
				warnNotice("       and uncheck 'Privilege Separation' so it inherits the user's rights."),
				warnNotice("  See README.md → 'Required permissions' for the full list."),
			}
		} else {
			c.notices = []notice{
				warnNotice(fmt.Sprintf("node %s has %d VMs but none are marked as templates", node, total)),
				warnNotice("  Fix: in the PVE web UI, right-click a VM → Convert to template."),
			}
		}
		if !offerBuild {
			c.manual = true
			return c
		}
	}

	if offerBuild {
		c.opts = append(c.opts, huh.NewOption("+ Build a new Ubuntu template now (pmox create-template)", createTemplateSentinel))
		c.initial = createTemplateSentinel
	}
	haveCurrent := false
	for _, t := range tmpls {
		label := fmt.Sprintf("%d  %s", t.VMID, t.Name)
		c.opts = append(c.opts, huh.NewOption(label, strconv.Itoa(t.VMID)))
		if current != "" && strconv.Itoa(t.VMID) == current {
			haveCurrent = true
		}
	}
	if len(tmpls) > 0 {
		c.initial = strconv.Itoa(tmpls[0].VMID)
	}
	if haveCurrent {
		c.initial = current
	}
	return c
}

func pickStorage(ctx context.Context, p prompter, client *pveclient.Client, node, current string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	return pickFromChoice(p, storageChoice(pools, err, node, current))
}

func storageChoice(pools []pveclient.Storage, err error, node, current string) fieldChoice {
	c := fieldChoice{title: "Default storage", manualLabel: "Default storage"}
	if err != nil {
		c.manual = true
		c.notices = []notice{
			warnNotice(fmt.Sprintf("could not list storage on node %s: %v", node, err)),
			warnNotice("  (the API token likely needs Datastore.Audit on /storage)"),
		}
		return c
	}
	if len(pools) == 0 {
		c.manual = true
		c.notices = []notice{warnNotice(fmt.Sprintf("no storage pools returned for node %s", node))}
		return c
	}
	// Filter to storages that can actually hold VM disk images.
	usable := pveclient.FilterStorage(pools, pveclient.Storage.SupportsVMDisks)
	if len(usable) == 0 {
		usable = pools
	}
	for _, s := range usable {
		c.opts = append(c.opts, huh.NewOption(storageLabel(s), s.Storage))
	}
	c.initial = firstNonEmpty(current, usable[0].Storage)
	return c
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
var selectSnippetStorageFn = tui.SelectOne

// pickSnippetStorage resolves the storage that pmox will use for
// cloud-init snippets. Decision tree: exactly one snippet-capable
// storage → silent; multiple → TUI picker; zero → offer to enable
// snippets on an existing dir-backed storage.
func pickSnippetStorage(ctx context.Context, p prompter, client snippetStoragePicker, node, current string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	if err != nil {
		p.Errf("could not list storage on node %s: %v\n", node, err)
		return "", nil
	}

	matches := pveclient.FilterStorage(pools, pveclient.Storage.SupportsSnippets)
	switch len(matches) {
	case 1:
		p.Printf("Snippet storage: %s\n", matches[0].Storage)
		return matches[0].Storage, nil
	case 0:
		return offerEnableSnippets(ctx, p, client, pools)
	}
	opts := make([]huh.Option[string], 0, len(matches))
	for _, s := range matches {
		label := storageLabel(s)
		opts = append(opts, huh.NewOption(label, s.Storage))
	}
	return selectSnippetStorageFn("Snippet storage", opts, firstNonEmpty(current, matches[0].Storage))
}

// offerEnableSnippets is the zero-match branch of pickSnippetStorage.
// It looks for a dir-backed storage to enable `snippets` on, defaults
// to "local" when present, and on confirmation issues
// UpdateStorageContent. Decline or absence prints the manual remediation
// and returns "" so credentials still save.
func offerEnableSnippets(ctx context.Context, p prompter, client snippetStoragePicker, pools []pveclient.Storage) (string, error) {
	target, ok := snippetEnableTarget(pools)
	if !ok {
		printSnippetManualRemediation(p)
		return "", nil
	}
	ans, err := p.Prompt(fmt.Sprintf("no storage supports snippets. enable snippets on %q? [Y/n]: ", target.Storage))
	if err != nil {
		return "", nil
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans != "" && ans != "y" && ans != "yes" {
		printSnippetManualRemediation(p)
		return "", nil
	}

	if err := client.UpdateStorageContent(ctx, target.Storage, snippetsEnabledContent(target)); err != nil {
		p.Errf("could not enable snippets on %q: %v\n", target.Storage, err)
		printSnippetManualRemediation(p)
		return "", nil
	}
	p.Printf("enabled snippets on %s\n", target.Storage)
	return target.Storage, nil
}

// snippetEnableTarget picks the directory-shaped storage to offer
// enabling `snippets` on when none supports it yet — "local" when
// present, else the first capable one. ok is false when no storage can
// host snippets at all.
func snippetEnableTarget(pools []pveclient.Storage) (pveclient.Storage, bool) {
	capable := pveclient.FilterStorage(pools, func(s pveclient.Storage) bool { return snippetCapableTypes[s.Type] })
	if len(capable) == 0 {
		return pveclient.Storage{}, false
	}
	target := capable[0]
	for _, s := range capable {
		if s.Storage == "local" {
			target = s
			break
		}
	}
	return target, true
}

// snippetsEnabledContent is target's content list with `snippets` added.
func snippetsEnabledContent(target pveclient.Storage) []string {
	content := target.ContentList()
	if !slices.Contains(content, "snippets") {
		content = append(content, "snippets")
	}
	return content
}

func printSnippetManualRemediation(p prompter) {
	printNotices(p, snippetRemediationNotices())
}

func snippetRemediationNotices() []notice {
	return []notice{
		warnNotice("no snippet storage configured."),
		warnNotice("  Fix: edit /etc/pve/storage.cfg on the PVE host and add 'snippets' to"),
		warnNotice("       the content= line of a directory-backed storage, then re-run"),
		warnNotice("       'pmox init' to record it."),
	}
}

func pickBridge(ctx context.Context, p prompter, client *pveclient.Client, node, current string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	bridges, err := client.ListBridges(dctx, node)
	return pickFromChoice(p, bridgeChoice(bridges, err, node, current))
}

func bridgeChoice(bridges []pveclient.Bridge, err error, node, current string) fieldChoice {
	c := fieldChoice{title: "Default bridge", manualLabel: "Default bridge"}
	if err != nil {
		c.manual = true
		c.notices = []notice{
			warnNotice(fmt.Sprintf("could not list bridges on node %s: %v", node, err)),
			warnNotice(fmt.Sprintf("  (the API token likely needs SDN.Audit or Sys.Audit on /nodes/%s)", node)),
		}
		return c
	}
	if len(bridges) == 0 {
		c.manual = true
		c.notices = []notice{warnNotice(fmt.Sprintf("no bridges returned for node %s", node))}
		return c
	}
	for _, b := range bridges {
		c.opts = append(c.opts, huh.NewOption(b.Iface, b.Iface))
	}
	c.initial = firstNonEmpty(current, bridges[0].Iface)
	return c
}
