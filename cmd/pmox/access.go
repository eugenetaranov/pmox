package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"sync"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/guestkeys"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// --- shared engine (CLI, interactive wizard, launch) ---

// accessEnv is everything an access operation needs for one cluster.
type accessEnv struct {
	cfg      *config.Config
	resolved *server.Resolved
	client   *pveclient.Client
	agent    guestkeys.Agent
	fs       accessreg.FS
	close    func()
}

// Seams so tests can run the access commands without a cluster.
var (
	accessConnectFn = func(ctx context.Context, cmd *cobra.Command) (*config.Config, *server.Resolved, *pveclient.Client, error) {
		s, err := connect(ctx, cmd, connectOptions{Stdin: os.Stdin})
		if err != nil {
			return nil, nil, nil, err
		}
		return s.Cfg, s.Resolved, s.Client, nil
	}
	guestAgentFn = func(c *pveclient.Client) guestkeys.Agent { return c }
	clusterVMsFn = func(ctx context.Context, c *pveclient.Client) ([]pveclient.Resource, error) {
		return c.ClusterResources(ctx, "vm")
	}
	syncWorkers   = 4
	errAccessSync = errors.New("some VMs could not be updated")
)

func openAccessEnv(ctx context.Context, cmd *cobra.Command) (*accessEnv, error) {
	cfg, resolved, client, err := accessConnectFn(ctx, cmd)
	if err != nil {
		return nil, err
	}
	fs, closeFS, err := openRegistryFn(ctx, resolved)
	if err != nil {
		return nil, err
	}
	return &accessEnv{cfg: cfg, resolved: resolved, client: client, agent: guestAgentFn(client), fs: fs, close: closeFS}, nil
}

// pmoxVMs returns the cluster's pmox-tagged VMs (no templates), by name.
func pmoxVMs(ctx context.Context, env *accessEnv) ([]pveclient.Resource, error) {
	all, err := clusterVMsFn(ctx, env.client)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	var out []pveclient.Resource
	for _, r := range all {
		if vm.HasPMOXTag(r.Tags) && !r.IsTemplate() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveVMArgs maps names/VMIDs to cluster resources, refusing VMs
// without the pmox tag unless force.
func resolveVMArgs(ctx context.Context, env *accessEnv, args []string, force bool) ([]pveclient.Resource, error) {
	all, err := clusterVMsFn(ctx, env.client)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	var out []pveclient.Resource
	for _, a := range args {
		var match *pveclient.Resource
		for i := range all {
			r := &all[i]
			if fmt.Sprint(r.VMID) == a || (r.Name == a && (match == nil || vm.HasPMOXTag(r.Tags))) {
				match = r
			}
		}
		if match == nil {
			return nil, fmt.Errorf("%w: VM %q not found", exitcode.ErrNotFound, a)
		}
		ref := &vm.Ref{VMID: match.VMID, Node: match.Node, Name: match.Name, Tags: match.Tags}
		if err := ref.RequirePMOXTag("share", force); err != nil {
			return nil, fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
		}
		out = append(out, *match)
	}
	return out, nil
}

// syncOutcome is what happened to one VM.
type syncOutcome struct {
	VM      pveclient.Resource
	Status  string // "updated" | "unchanged" | "pending" | "failed"
	Detail  string
	Missing []string // granted people with no published key
}

// guestUserFor is the login user whose authorized_keys pmox manages.
func guestUserFor(env *accessEnv, vmid int) string {
	user, _, _, err := resolveGuestIdentity(env.resolved.URL, vmid, "", "", env.resolved.Server)
	if err != nil || user == "" {
		return firstNonEmpty(env.resolved.Server.User, "ubuntu")
	}
	return user
}

// syncVMs brings each VM's managed block in line with acc, with bounded
// concurrency. Outcomes keep vms' order.
func syncVMs(ctx context.Context, env *accessEnv, acc *accessreg.Access, keys []accessreg.PublishedKey, vms []pveclient.Resource) []syncOutcome {
	out := make([]syncOutcome, len(vms))
	sem := make(chan struct{}, syncWorkers)
	var wg sync.WaitGroup
	for i, r := range vms {
		wg.Add(1)
		go func(i int, r pveclient.Resource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = syncOne(ctx, env, acc, keys, r)
		}(i, r)
	}
	wg.Wait()
	return out
}

func syncOne(ctx context.Context, env *accessEnv, acc *accessreg.Access, keys []accessreg.PublishedKey, r pveclient.Resource) syncOutcome {
	lines, missing := acc.KeysFor(r.VMID, keys)
	o := syncOutcome{VM: r, Missing: missing}
	if !r.IsRunning() {
		o.Status, o.Detail = "pending", "stopped — run 'pmox access sync' after it starts"
		return o
	}
	tgt := guestkeys.Target{Node: r.Node, VMID: r.VMID, User: guestUserFor(env, r.VMID)}
	res, err := guestkeys.Apply(ctx, env.agent, tgt, lines)
	switch {
	case err == nil && res.Changed:
		o.Status, o.Detail = "updated", describeKeys(lines)
	case err == nil:
		o.Status, o.Detail = "unchanged", describeKeys(lines)
	case errors.Is(err, pveclient.ErrVMNotRunning):
		o.Status, o.Detail = "pending", "stopped — run 'pmox access sync' after it starts"
	case errors.Is(err, pveclient.ErrAgentNotRunning):
		o.Status, o.Detail = "pending", "guest agent not responding — run 'pmox access sync' once the VM is fully up"
	case errors.Is(err, pveclient.ErrForbidden):
		o.Status, o.Detail = "failed", "the API token lacks guest-agent file access — grant VM.GuestAgent.FileRead + VM.GuestAgent.FileWrite (PVE 9) or VM.Monitor (PVE 8) on this VM"
	default:
		o.Status, o.Detail = "failed", err.Error()
	}
	return o
}

// describeKeys names the people whose key lines are given.
func describeKeys(lines []string) string {
	if len(lines) == 0 {
		return "no shared keys"
	}
	return "keys: " + strings.Join(keyNames(lines), ", ")
}

// keyNames extracts registry names from managed key lines.
func keyNames(lines []string) []string {
	names := make([]string, 0, len(lines))
	for _, l := range lines {
		f := strings.Fields(l)
		name := "(unlabelled)"
		if len(f) >= 3 {
			name = strings.TrimPrefix(f[len(f)-1], accessreg.LabelPrefix)
		}
		names = append(names, name)
	}
	return names
}

func printOutcomes(w io.Writer, outcomes []syncOutcome) error {
	failed := 0
	for _, o := range outcomes {
		mark := map[string]string{"updated": "✓", "unchanged": "=", "pending": "…", "failed": "✗"}[o.Status]
		line := fmt.Sprintf("%s %s (%d): %s", mark, o.VM.Name, o.VM.VMID, o.Detail)
		if o.Status == "failed" {
			failed++
			line = tui.Warnf(line)
		}
		fmt.Fprintln(w, line)
		if len(o.Missing) > 0 {
			fmt.Fprintln(w, tui.Warnf(fmt.Sprintf("    granted but not published (skipped): %s — they need to run 'pmox key publish'", strings.Join(o.Missing, ", "))))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%w (%d failed)", errAccessSync, failed)
	}
	return nil
}

// --- commands ---

type accessFlags struct {
	to     string
	allVMs bool
	force  bool
}

func newAccessCmd() *cobra.Command {
	return nounGroup("access", "Share VMs with other people", `Decide who can SSH into which pmox VMs.

People publish their SSH public key once with 'pmox key publish'. A
cluster admin then grants them VMs; pmox writes their keys into a
managed block of each VM's ~/.ssh/authorized_keys through the QEMU guest
agent. The desired state lives on the cluster in /etc/pve/pmox, so
every workstation sees the same grants.

'pmox access setup' picks people and VMs interactively.`,
		newAccessSetupCmd(), newAccessGrantCmd(), newAccessRevokeCmd(), newAccessListCmd(), newAccessSyncCmd())
}

func newAccessSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Choose people and their VMs interactively",
		Args:  cobra.NoArgs,
		RunE:  runAccessInteractive,
	}
}

func newAccessGrantCmd() *cobra.Command {
	f := &accessFlags{}
	cmd := &cobra.Command{
		Use:   "grant [vm...] --to <name>",
		Short: "Give a person SSH access to VMs",
		Example: `  pmox access grant web1 db1 --to bob
  pmox access grant --all-vms --to carol    # every pmox VM, including future ones`,
		RunE: func(cmd *cobra.Command, args []string) error { return runAccessChange(cmd, args, f, true) },
	}
	addAccessChangeFlags(cmd, f)
	return cmd
}

func newAccessRevokeCmd() *cobra.Command {
	f := &accessFlags{}
	cmd := &cobra.Command{
		Use:   "revoke [vm...] --to <name>",
		Short: "Remove a person's SSH access to VMs",
		Example: `  pmox access revoke web1 --to bob
  pmox access revoke --all-vms --to bob     # remove all of bob's access`,
		RunE: func(cmd *cobra.Command, args []string) error { return runAccessChange(cmd, args, f, false) },
	}
	addAccessChangeFlags(cmd, f)
	return cmd
}

func addAccessChangeFlags(cmd *cobra.Command, f *accessFlags) {
	cmd.Flags().StringVar(&f.to, "to", "", "registry name of the person (as published with 'pmox key publish')")
	cmd.Flags().BoolVar(&f.allVMs, "all-vms", false, "every pmox VM, including ones launched later")
	cmd.Flags().BoolVar(&f.force, "force", false, "allow VMs without the pmox tag")
}

func runAccessChange(cmd *cobra.Command, args []string, f *accessFlags, grant bool) error {
	ctx := cmd.Context()
	// On a terminal, whatever wasn't given is asked for (the palette runs
	// 'access grant' with no arguments); scripts must pass it.
	prompt := tui.Interactive() && (f.to == "" || (!f.allVMs && len(args) == 0))
	if !prompt {
		if f.to == "" {
			return fmt.Errorf("%w: --to <name> is required", exitcode.ErrUserInput)
		}
		if f.allVMs == (len(args) > 0) {
			return fmt.Errorf("%w: name one or more VMs, or pass --all-vms", exitcode.ErrUserInput)
		}
	}
	env, err := openAccessEnv(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()
	if prompt {
		if args, err = promptAccessChange(ctx, env, f, args, grant); err != nil {
			return err
		}
	}
	if err := accessreg.ValidName(f.to); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}

	if grant {
		if _, err := accessreg.GetKey(ctx, env.fs, f.to); errors.Is(err, accessreg.ErrNotPublished) {
			return fmt.Errorf("%w: %q has no published key — they need to run 'pmox key publish' first", exitcode.ErrNotFound, f.to)
		} else if err != nil {
			return err
		}
	}
	var targets []pveclient.Resource
	if f.allVMs {
		targets, err = pmoxVMs(ctx, env)
	} else {
		targets, err = resolveVMArgs(ctx, env, args, f.force)
	}
	if err != nil {
		return err
	}

	acc, err := accessreg.UpdateAccess(ctx, env.fs, func(a *accessreg.Access) error {
		ids := make([]int, len(targets))
		for i, t := range targets {
			ids[i] = t.VMID
		}
		switch {
		case grant && f.allVMs:
			a.GrantAll(f.to)
		case grant:
			a.GrantVMs(f.to, ids...)
		case f.allVMs:
			a.RevokeAll(f.to)
		default:
			a.RevokeVMs(f.to, ids...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !grant && !f.allVMs {
		if g := acc.People[f.to]; g != nil && g.AllVMs {
			fmt.Fprintln(cmd.ErrOrStderr(), tui.Warnf(fmt.Sprintf("note: %s still has access to all pmox VMs; use --all-vms to revoke that", f.to)))
		}
	}
	keys, _, err := accessreg.ListKeys(ctx, env.fs)
	if err != nil {
		return err
	}
	return printOutcomes(cmd.OutOrStdout(), syncVMs(ctx, env, acc, keys, targets))
}

func newAccessSyncCmd() *cobra.Command {
	f := &accessFlags{}
	cmd := &cobra.Command{
		Use:   "sync [vm...]",
		Short: "Update VMs to match the access registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openAccessEnv(ctx, cmd)
			if err != nil {
				return err
			}
			defer env.close()
			acc, err := accessreg.ReadAccess(ctx, env.fs)
			if err != nil {
				return err
			}
			keys, _, err := accessreg.ListKeys(ctx, env.fs)
			if err != nil {
				return err
			}
			var targets []pveclient.Resource
			if len(args) == 0 {
				targets, err = pmoxVMs(ctx, env)
			} else {
				targets, err = resolveVMArgs(ctx, env, args, f.force)
			}
			if err != nil {
				return err
			}
			return printOutcomes(cmd.OutOrStdout(), syncVMs(ctx, env, acc, keys, targets))
		},
	}
	cmd.Flags().BoolVar(&f.force, "force", false, "allow VMs without the pmox tag")
	return cmd
}

func newAccessListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show [vm]",
		Aliases: []string{"list", "ls"},
		Short:   "Show who can reach which VMs",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runAccessList,
	}
}

func runAccessList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	env, err := openAccessEnv(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()
	acc, err := accessreg.ReadAccess(ctx, env.fs)
	if err != nil {
		return err
	}
	keys, badKeys, err := accessreg.ListKeys(ctx, env.fs)
	if err != nil {
		return err
	}
	var vms []pveclient.Resource
	if len(args) == 1 {
		vms, err = resolveVMArgs(ctx, env, args, true)
	} else {
		vms, err = pmoxVMs(ctx, env)
	}
	if err != nil {
		return err
	}

	published := map[string]accessreg.PublishedKey{}
	names := map[string]bool{}
	for _, k := range keys {
		published[k.Name] = k
		names[k.Name] = true
	}
	for n := range acc.People {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	fmt.Fprintln(w, "People")
	if len(sorted) == 0 {
		fmt.Fprintln(w, "  nobody yet — people publish their key with 'pmox key publish'")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "  PERSON\tKEY\tSHARED VMS")
		for _, n := range sorted {
			keyInfo := "not published"
			if k, ok := published[n]; ok {
				keyInfo = k.Fingerprint
			}
			shared := grantSummary(acc.People[n], vms)
			if shared == "none" {
				shared = fmt.Sprintf("none yet — pmox access grant <vm> --to %s", n)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", n, keyInfo, shared)
		}
		_ = tw.Flush()
	}
	for _, e := range badKeys {
		fmt.Fprintln(w, tui.Warnf("  unreadable key file: "+e.Error()))
	}

	fmt.Fprintln(w, "\nVMs")
	if len(vms) == 0 {
		fmt.Fprintln(w, "  no pmox VMs")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "  VM\tVMID\tSHARED WITH\tON THE VM")
		for _, r := range vms {
			want, _ := acc.KeysFor(r.VMID, keys)
			status := "not checked (stopped)"
			if r.IsRunning() {
				_, have, rerr := guestkeys.Read(ctx, env.agent, guestkeys.Target{Node: r.Node, VMID: r.VMID, User: guestUserFor(env, r.VMID)})
				switch {
				case rerr != nil:
					status = "could not read: " + rerr.Error()
				case strings.Join(have, "\n") == strings.Join(want, "\n"):
					status = "up to date"
				default:
					status = "OUT OF SYNC — run 'pmox access sync " + r.Name + "'"
				}
			}
			who := strings.Join(keyNames(want), ", ")
			if who == "" {
				who = "nobody"
			}
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\n", r.Name, r.VMID, who, status)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(w, tui.Subtitle("\nShared access is on top of each VM's own launch key: whoever launched a VM can always reach it."))
	return nil
}

func grantSummary(g *accessreg.Grant, vms []pveclient.Resource) string {
	if g == nil || (!g.AllVMs && len(g.VMs) == 0) {
		return "none"
	}
	if g.AllVMs {
		return "all pmox VMs"
	}
	byID := map[int]string{}
	for _, r := range vms {
		byID[r.VMID] = r.Name
	}
	parts := make([]string, 0, len(g.VMs))
	for _, id := range g.VMs {
		if n, ok := byID[id]; ok {
			parts = append(parts, n)
		} else {
			parts = append(parts, fmt.Sprintf("%d (gone)", id))
		}
	}
	return strings.Join(parts, ", ")
}

// runAccessInteractive is filled in with the wizard (access_wizard.go).
func runAccessInteractive(cmd *cobra.Command, _ []string) error {
	if !tui.Interactive() {
		return fmt.Errorf("%w: 'pmox access setup' needs a terminal; use 'pmox access grant|revoke|list|sync' in scripts", exitcode.ErrUserInput)
	}
	return runAccessWizard(cmd)
}

// applySharedAccessFn is a seam so launch/clone tests never dial a node.
var applySharedAccessFn = applySharedAccess

// applySharedAccess pushes the registry's grants for a just-launched VM:
// everyone with all-VM access plus anyone whose list names its VMID.
// Best-effort — silent when there's no registry (or no node SSH to reach
// it), a warning on failure, and it never fails the launch.
func applySharedAccess(ctx context.Context, stderr io.Writer, client *pveclient.Client, serverURL string, vmid int, node, name string) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	r, err := server.Resolve(ctx, server.Options{Cfg: cfg, Flag: serverURL})
	if err != nil || !r.HasNodeSSH() {
		return
	}
	warn := func(detail string) {
		fmt.Fprintln(stderr, tui.Warnf(fmt.Sprintf("warning: could not apply shared access to %s: %s — run 'pmox access sync %s'", name, detail, name)))
	}
	fs, closeFS, err := openRegistryFn(ctx, r)
	if err != nil {
		warn(err.Error())
		return
	}
	defer closeFS()
	acc, err := accessreg.ReadAccess(ctx, fs)
	if err != nil {
		warn(err.Error())
		return
	}
	keys, _, err := accessreg.ListKeys(ctx, fs)
	if err != nil {
		warn(err.Error())
		return
	}
	if lines, _ := acc.KeysFor(vmid, keys); len(lines) == 0 {
		return
	}
	env := &accessEnv{cfg: cfg, resolved: r, client: client, agent: guestAgentFn(client), fs: fs}
	o := syncOne(ctx, env, acc, keys, pveclient.Resource{VMID: vmid, Node: node, Name: name, Status: "running"})
	switch o.Status {
	case "updated", "unchanged":
		fmt.Fprintf(stderr, "shared access: %s\n", o.Detail)
	default:
		warn(o.Detail)
	}
}

// Seams over the grant/revoke pickers.
var (
	pickPersonFn = tui.Select
	pickVMsFn    = tui.SelectMulti
)

// promptAccessChange asks for the person and VMs a grant/revoke didn't
// name. Grant offers everyone with a published key and every pmox VM;
// revoke offers only people with access and only what they can reach.
func promptAccessChange(ctx context.Context, env *accessEnv, f *accessFlags, args []string, grant bool) ([]string, error) {
	keys, _, err := accessreg.ListKeys(ctx, env.fs)
	if err != nil {
		return nil, err
	}
	acc, err := accessreg.ReadAccess(ctx, env.fs)
	if err != nil {
		return nil, err
	}
	vms, err := pmoxVMs(ctx, env)
	if err != nil {
		return nil, err
	}

	if f.to == "" {
		var opts []huh.Option[string]
		if grant {
			for _, k := range keys {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%-12s %s   %s", k.Name, k.Fingerprint, grantSummary(acc.People[k.Name], vms)), k.Name))
			}
			if len(opts) == 0 {
				return nil, fmt.Errorf("%w: nobody has published a key yet — people run 'pmox key publish' first", exitcode.ErrNotFound)
			}
		} else {
			names := make([]string, 0, len(acc.People))
			for n := range acc.People {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%-12s %s", n, grantSummary(acc.People[n], vms)), n))
			}
			if len(opts) == 0 {
				return nil, fmt.Errorf("%w: nobody has been granted access", exitcode.ErrNotFound)
			}
		}
		verb := "Grant access to"
		if !grant {
			verb = "Revoke access from"
		}
		if f.to, err = pickPersonFn(verb+":", opts); err != nil {
			return nil, err
		}
	}

	if f.allVMs || len(args) > 0 {
		return args, nil
	}
	current := acc.People[f.to]
	var opts []huh.Option[string]
	if grant || (current != nil && current.AllVMs) {
		opts = append(opts, huh.NewOption("★ All pmox VMs, including future ones", allVMsValue))
	}
	for _, r := range vms {
		if !grant && (current == nil || (!current.AllVMs && !slices.Contains(current.VMs, r.VMID))) {
			continue
		}
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-20s %-6d %s", r.Name, r.VMID, r.Status), strconv.Itoa(r.VMID)))
	}
	if len(opts) == 0 {
		return nil, fmt.Errorf("%w: %s has no VM access to revoke", exitcode.ErrNotFound, f.to)
	}
	title := fmt.Sprintf("VMs to give %s access to (space to select)", f.to)
	if !grant {
		title = fmt.Sprintf("VMs to remove %s's access from (space to select)", f.to)
	}
	picked, err := pickVMsFn(title, opts)
	if err != nil {
		return nil, err
	}
	if slices.Contains(picked, allVMsValue) {
		f.allVMs = true
		return nil, nil
	}
	return picked, nil
}
