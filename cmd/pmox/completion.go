package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/paths"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// Dynamic shell completion (openspec/specs/shell-completion): VM,
// context, template and placement names. A completion never prompts,
// never writes config or pins, prints nothing but candidates, gives up
// after completionTimeout, and reuses a cluster answer for
// completionCacheTTL so repeated Tabs don't each call the API.

const (
	completionTimeout  = 2 * time.Second
	completionCacheTTL = 10 * time.Second
)

// completionVM is one VM as completion needs it.
type completionVM struct {
	Name   string `json:"name"`
	VMID   int    `json:"vmid"`
	Status string `json:"status"`
	Node   string `json:"node"`
	PMOX   bool   `json:"pmox"`
	Templ  bool   `json:"template"`
}

// completionCache is the cached cluster answer for one context.
type completionCache struct {
	At     time.Time           `json:"at"`
	VMs    []completionVM      `json:"vms,omitempty"`
	Names  map[string][]string `json:"names,omitempty"` // nodes, storage:<node>, bridges:<node>
	loaded bool
}

// completionSession is a restricted, read-only connection for one
// completion: nil when pmox isn't configured or the server can't be
// resolved without asking.
type completionSession struct {
	client *pveclient.Client
	url    string
	srv    *config.Server
}

// completionConnectFn opens the restricted connection. A seam for tests.
var completionConnectFn = completionConnect

func completionConnect(ctx context.Context) *completionSession {
	tui.SetNoInput(true)
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:        cfg,
		Flag:       serverFlag,
		Context:    contextFlag,
		Env:        os.Getenv("PMOX_SERVER"),
		ContextEnv: os.Getenv("PMOX_CONTEXT"),
	})
	if err != nil {
		return nil
	}
	// Read-only: a stored pin is enforced, nothing is pinned or printed.
	pin, err := checkTLSPin(ctx, io.Discard, nil, resolved.URL, resolved.Server, pinReadOnly)
	if err != nil {
		return nil
	}
	return &completionSession{client: newAPIClient(resolved.URL, resolved.Server, resolved.Secret, pin), url: resolved.URL, srv: resolved.Server}
}

// completionCachePath is the cache file for the server at url.
func completionCachePath(url string) (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", err
	}
	slug, err := config.Slug(url)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "completion", slug+".json"), nil
}

func readCompletionCache(url string) completionCache {
	var c completionCache
	p, err := completionCachePath(url)
	if err != nil {
		return c
	}
	data, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(data, &c) != nil || time.Since(c.At) > completionCacheTTL || time.Since(c.At) < 0 {
		return completionCache{}
	}
	c.loaded = true
	return c
}

func writeCompletionCache(url string, c completionCache) {
	p, err := completionCachePath(url)
	if err != nil {
		return
	}
	if c.At.IsZero() {
		c.At = time.Now()
	}
	data, err := json.Marshal(c)
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o600)
}

// invalidateCompletionCache drops every cached answer, after a command
// that changed which VMs exist or their state.
func invalidateCompletionCache() {
	if dir, err := paths.StateDir(); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, "completion"))
	}
}

// completionMutators are the commands after which cached VM names or
// states are stale.
var completionMutators = []string{"launch", "clone", "delete", "start", "stop", "restart"}

// completionVMs returns the cluster's VMs and templates (nil when they
// can't be had quickly and quietly).
func completionVMs(ctx context.Context) []completionVM {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	s := completionConnectFn(ctx)
	if s == nil {
		return nil
	}
	cache := readCompletionCache(s.url)
	if cache.loaded && cache.VMs != nil {
		return cache.VMs
	}
	resources, err := s.client.QEMUResources(ctx)
	if err != nil {
		return nil
	}
	vms := make([]completionVM, 0, len(resources))
	for _, r := range resources {
		vms = append(vms, completionVM{Name: r.Name, VMID: r.VMID, Status: r.Status, Node: r.Node, PMOX: vm.HasPMOXTag(r.Tags), Templ: r.IsTemplate()})
	}
	cache.VMs = vms
	writeCompletionCache(s.url, cache)
	return vms
}

// completionNames returns a cached list of names of one kind ("nodes",
// "storage", "bridges"), fetched with fetch on a miss.
func completionNames(ctx context.Context, kind string, node string, fetch func(ctx context.Context, c *pveclient.Client, node string) ([]string, error)) []string {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	s := completionConnectFn(ctx)
	if s == nil {
		return nil
	}
	if node == "" && s.srv != nil {
		node = s.srv.Node
	}
	key := kind + ":" + node
	cache := readCompletionCache(s.url)
	if names, ok := cache.Names[key]; cache.loaded && ok {
		return names
	}
	names, err := fetch(ctx, s.client, node)
	if err != nil {
		return nil
	}
	if cache.Names == nil {
		cache.Names = map[string][]string{}
	}
	cache.Names[key] = names
	writeCompletionCache(s.url, cache)
	return names
}

// vmFilter narrows the VMs a command is offered.
type vmFilter func(completionVM) bool

func anyVM(completionVM) bool          { return true }
func stoppedVM(v completionVM) bool    { return v.Status != "running" }
func runningVM(v completionVM) bool    { return v.Status == "running" }
func isDigits(s string) bool           { _, err := strconv.Atoi(s); return err == nil }
func vmDesc(v completionVM) string     { return "vmid " + strconv.Itoa(v.VMID) + ", " + v.Status }
func idDesc(v completionVM) string     { return v.Name + ", " + v.Status }
func comp(value, desc string) string   { return value + "\t" + desc }
func noComp() cobra.ShellCompDirective { return cobra.ShellCompDirectiveNoFileComp }

// vmCandidates lists pmox VMs passing keep and not in taken: names, or
// VMIDs when toComplete is digits.
func vmCandidates(ctx context.Context, keep vmFilter, taken []string, toComplete string) []string {
	var out []string
	for _, v := range completionVMs(ctx) {
		if !v.PMOX || v.Templ || !keep(v) || slices.Contains(taken, v.Name) || slices.Contains(taken, strconv.Itoa(v.VMID)) {
			continue
		}
		if toComplete != "" && isDigits(toComplete) {
			out = append(out, comp(strconv.Itoa(v.VMID), idDesc(v)))
		} else {
			out = append(out, comp(v.Name, vmDesc(v)))
		}
	}
	return out
}

// completeVMs completes <name|vmid> arguments: the first one only, or
// with multi every one, leaving out VMs already named. after is the
// directive once the VM argument(s) are taken.
func completeVMs(keep vmFilter, multi bool, after cobra.ShellCompDirective) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if !multi && len(args) > 0 {
			return nil, after
		}
		return vmCandidates(cmd.Context(), keep, args, toComplete), noComp()
	}
}

// completeVMTargets completes an argument that may be a <vm>:<path>
// target: VM names followed by ":" (no space after), with the shell's
// file completion when nothing matches. The path after ":" isn't
// completed (that would take an SSH round-trip per Tab).
func completeVMTargets(localArgs int, dirsOnly bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		files := cobra.ShellCompDirectiveDefault
		if dirsOnly {
			files = cobra.ShellCompDirectiveFilterDirs
		}
		if len(args) < localArgs {
			return nil, files
		}
		if len(args) >= 2 || strings.Contains(toComplete, ":") {
			return nil, noComp()
		}
		// The other side is already the remote one: this one is local.
		if slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, ":") }) {
			return nil, files
		}
		var out []string
		for _, c := range vmCandidates(cmd.Context(), anyVM, nil, toComplete) {
			name, desc, _ := strings.Cut(c, "\t")
			out = append(out, comp(name+":", desc))
		}
		if localArgs > 0 {
			return out, cobra.ShellCompDirectiveNoSpace | noComp()
		}
		return out, cobra.ShellCompDirectiveNoSpace
	}
}

// completeMounts completes the <vm>:<path> of existing background mounts.
func completeMounts(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, noComp()
	}
	dir, err := mount.StateDir()
	if err != nil {
		return nil, noComp()
	}
	recs, err := mount.List(dir)
	if err != nil {
		return nil, noComp()
	}
	var out []string
	for _, r := range recs {
		out = append(out, comp(r.VMName+":"+r.RemotePath, r.LocalPath))
	}
	return out, noComp()
}

// contextNames lists the configured contexts.
func contextNames() []string {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range cfg.Contexts() {
		out = append(out, comp(c.Name, c.URL))
	}
	return out
}

// completeContexts completes a context name as the first argument.
func completeContexts(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, noComp()
	}
	return contextNames(), noComp()
}

func completeContextFlag(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return contextNames(), noComp()
}

func completeTemplateFlag(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, v := range completionVMs(cmd.Context()) {
		if v.Templ {
			out = append(out, comp(v.Name, "vmid "+strconv.Itoa(v.VMID)))
		}
	}
	return out, noComp()
}

func completeNodeFlag(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionNames(cmd.Context(), "nodes", "-", func(ctx context.Context, c *pveclient.Client, _ string) ([]string, error) {
		nodes, err := c.ListNodes(ctx)
		var out []string
		for _, n := range nodes {
			out = append(out, comp(n.Node, n.Status))
		}
		return out, err
	}), noComp()
}

// flagNode is the node the placement flags complete for: --node when
// given, else the configured default ("" here; completionNames fills it).
func flagNode(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("node"); f != nil {
		return f.Value.String()
	}
	return ""
}

func completeStorageFlag(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionNames(cmd.Context(), "storage", flagNode(cmd), func(ctx context.Context, c *pveclient.Client, node string) ([]string, error) {
		list, err := c.ListStorage(ctx, node)
		var out []string
		for _, s := range list {
			out = append(out, comp(s.Storage, s.Type))
		}
		return out, err
	}), noComp()
}

func completeBridgeFlag(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionNames(cmd.Context(), "bridges", flagNode(cmd), func(ctx context.Context, c *pveclient.Client, node string) ([]string, error) {
		list, err := c.ListBridges(ctx, node)
		var out []string
		for _, b := range list {
			out = append(out, comp(b.Iface, b.CIDR))
		}
		return out, err
	}), noComp()
}

// argCompleters maps a command path (without "pmox ") to its argument
// completion. Shortcuts and their canonical commands are listed by the
// canonical name: wireCompletions follows the shortcut annotation.
func argCompleters() map[string]func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	one := completeVMs(anyVM, false, noComp())
	many := completeVMs(anyVM, true, noComp())
	return map[string]func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective){
		"info":           one, // the root 'info': a VM, or the host when bare
		"vm shell":       one,
		"vm info":        one,
		"vm ssh-config":  one,
		"vm use":         one,
		"vm clone":       one,
		"vm apply":       one,
		"vm exec":        completeVMs(anyVM, false, cobra.ShellCompDirectiveDefault),
		"vm start":       completeVMs(stoppedVM, false, noComp()),
		"vm stop":        completeVMs(runningVM, true, noComp()),
		"vm restart":     many,
		"vm delete":      many,
		"vm resize":      one,
		"vm cp":          completeVMTargets(0, false),
		"vm sync":        completeVMTargets(0, false),
		"mount":          completeVMTargets(1, true),
		"mount create":   completeVMTargets(1, true),
		"mount delete":   completeMounts,
		"mount restart":  completeMounts,
		"yardo push":     one,
		"yardo gitkey":   completeVMs(anyVM, false, cobra.ShellCompDirectiveDefault),
		"yardo apply":    many,
		"yardo verify":   many,
		"access grant":   many,
		"access revoke":  many,
		"access sync":    many,
		"access show":    one,
		"context use":    completeContexts,
		"context rename": completeContexts,
		"context delete": completeContexts,
		"context edit":   completeContexts,
	}
}

// wireCompletions attaches argument and flag completion to every
// command under root.
func wireCompletions(root *cobra.Command) {
	_ = root.RegisterFlagCompletionFunc("context", completeContextFlag)
	args := argCompleters()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		path := strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
		if canon := c.Annotations[canonicalAnnotation]; canon != "" {
			path = canon
		}
		old := c.Annotations[deprecatedAnnotation] != ""
		if old { // deprecated forms complete nothing
			path = ""
		}
		if fn, ok := args[path]; ok {
			c.ValidArgsFunction = fn
		}
		for flag, fn := range map[string]func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective){
			"template": completeTemplateFlag, "node": completeNodeFlag,
			"storage": completeStorageFlag, "snippet-storage": completeStorageFlag, "bridge": completeBridgeFlag,
		} {
			if !old && c.Flags().Lookup(flag) != nil {
				_ = c.RegisterFlagCompletionFunc(flag, fn)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
