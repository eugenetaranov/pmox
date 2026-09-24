package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/snippet"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// deleteTaskTimeout bounds each underlying PVE task (shutdown/stop,
// destroy). Matches the launch command's 120s clone/WaitTask budget.
const deleteTaskTimeout = 120 * time.Second

type deleteFlags struct {
	force bool
	hard  bool
	yes   bool
}

func newDeleteCmd() *cobra.Command {
	f := &deleteFlags{}
	cmd := &cobra.Command{
		Use:   "delete [name|vmid ...]",
		Short: "Stop and destroy pmox-launched VMs",
		Long: `Delete one or more VMs on the resolved Proxmox cluster. Each argument
may be a VM name (e.g. "web1") or numeric VMID (e.g. "104"). If none are
given, pmox auto-selects the only pmox VM when one exists, or shows a
multi-select picker (space to toggle, enter to confirm) when several do.

Before issuing any destructive API call the command prints a summary of
the resolved VM and requires an interactive y/N confirmation (default No).
Pass --yes / -y or set PMOX_ASSUME_YES=1 to skip the prompt for scripted
and CI use. When stdin is not a TTY and no bypass is set, the command
refuses to proceed.

By default, delete refuses to act on VMs that are not tagged "pmox".
Since pmox launch tags every VM it creates, this rule means delete
will only touch VMs pmox launched — hand-managed VMs are protected
from accidental destruction.

--force bypasses the tag check, allowing delete on untagged VMs — reach
for it when the VM is hand-managed. --hard uses a hard "stop" (power off)
instead of a graceful ACPI "shutdown" — reach for it when the guest is
not responding to ACPI. The two are independent; either can be used
alone. Both are orthogonal to --yes: using them alone still prompts.

If the VM has already been destroyed, delete exits 0 with a note on
stderr so scripted loops are idempotent.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDelete(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.force, "force", false, "bypass the pmox tag check (allow deleting untagged VMs)")
	cmd.Flags().BoolVar(&f.hard, "hard", false, "hard power-off instead of graceful ACPI shutdown")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "skip the confirmation prompt (env: PMOX_ASSUME_YES)")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string, f *deleteFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	assumeYes := f.yes || envBool("PMOX_ASSUME_YES")

	var confirmer tui.Confirmer
	if assumeYes {
		confirmer = tui.AlwaysConfirmer{}
	} else if tui.StdinIsTerminal() {
		confirmer = tui.NewTTYConfirmer(os.Stdin, cmd.ErrOrStderr())
	} else {
		return fmt.Errorf("refusing to delete: stdin is not a TTY and --yes was not passed; re-run with --yes (or PMOX_ASSUME_YES=1) for non-interactive use")
	}

	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}

	targets, err := resolveTargetArgs(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return executeDelete(ctx, cmd, client, targets, f, confirmer)
}

// vmPickFn / vmPickMultiFn are the single- and multi-select pickers used
// to resolve implicit targets. Tests override them to bypass the real TUI.
var (
	vmPickFn      = vm.Pick
	vmPickMultiFn = vm.PickMulti
)

// resolveTargetArg returns a concrete <name|vmid> string for commands
// that accept an optional positional target. When args has one element
// it's returned as-is; otherwise vmPickFn is consulted and the picked
// VM's VMID is returned (as a string) for feeding back into vm.Resolve.
func resolveTargetArg(ctx context.Context, client *pveclient.Client, args []string, stderr io.Writer) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	ref, err := vmPickFn(ctx, client)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(ref.VMID), nil
}

// resolveTargetArgs returns one or more <name|vmid> targets for commands
// that accept multiple. Explicit args pass through; with none, the
// multi-select picker is consulted.
func resolveTargetArgs(ctx context.Context, client *pveclient.Client, args []string, stderr io.Writer) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	refs, err := vmPickMultiFn(ctx, client)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = strconv.Itoa(r.VMID)
	}
	return out, nil
}

// executeDelete resolves one or more targets, confirms once, then
// destroys each. It holds the command logic without server/config wiring
// so tests can drive it with a fake client directly.
func executeDelete(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, args []string, f *deleteFlags, confirmer tui.Confirmer) error {
	refs, err := resolveDeleteRefs(ctx, client, args, f)
	if err != nil {
		return err
	}
	if err := confirmDelete(ctx, cmd, refs, f, confirmer); err != nil {
		return err
	}

	spinner := newDeleteSpinner(cmd.ErrOrStderr())
	var failed int
	for _, ref := range refs {
		if err := destroyVM(ctx, cmd, client, ref, f, spinner); err != nil {
			if len(refs) == 1 {
				return err
			}
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "delete %q (vmid %d) failed: %v\n", ref.Name, ref.VMID, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d VMs failed to delete", failed, len(refs))
	}
	return nil
}

// resolveDeleteRefs resolves every target and enforces the pmox-tag guard
// (unless --force) before anything is destroyed.
func resolveDeleteRefs(ctx context.Context, client *pveclient.Client, args []string, f *deleteFlags) ([]*vm.Ref, error) {
	refs := make([]*vm.Ref, 0, len(args))
	for _, arg := range args {
		ref, err := vm.Resolve(ctx, client, arg)
		if err != nil {
			return nil, err
		}
		if !f.force && !vm.HasPMOXTag(ref.Tags) {
			return nil, fmt.Errorf("refusing to delete VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// confirmDelete asks once for the whole set. The single-VM prompt keeps
// its original wording; multiple VMs are listed explicitly so a bulk
// delete's blast radius is visible before the y/N.
func confirmDelete(ctx context.Context, cmd *cobra.Command, refs []*vm.Ref, f *deleteFlags, confirmer tui.Confirmer) error {
	if f.yes {
		return nil
	}
	verb := "delete"
	if f.force {
		verb = "FORCE-delete"
	}
	var prompt string
	if len(refs) == 1 {
		ref := refs[0]
		tags := ref.Tags
		if tags == "" {
			tags = "<none>"
		}
		prompt = fmt.Sprintf("About to %s VM %q (vmid %d, node %s, tags %s)\n", verb, ref.Name, ref.VMID, ref.Node, tags)
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "About to %s %d VMs:\n", verb, len(refs))
		for _, ref := range refs {
			fmt.Fprintf(&b, "  - %s (vmid %d, node %s)\n", ref.Name, ref.VMID, ref.Node)
		}
		prompt = b.String()
	}
	if f.hard {
		prompt += "This will use hard stop (no graceful shutdown).\n"
	}
	if f.force {
		prompt += "This bypasses the pmox tag check.\n"
	}
	prompt += "Continue? [y/N]: "
	ok, err := confirmer.Confirm(ctx, prompt)
	if err != nil {
		return fmt.Errorf("confirmation: %w", err)
	}
	if !ok {
		return fmt.Errorf("delete cancelled")
	}
	return nil
}

// destroyVM stops (if running), cleans the snippet, and destroys one VM.
func destroyVM(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, ref *vm.Ref, f *deleteFlags, spinner stepProgress) error {
	status, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "VM %q (vmid %d) is already gone\n", ref.Name, ref.VMID)
			return nil
		}
		return fmt.Errorf("get status for vm %d: %w", ref.VMID, err)
	}

	// Fetch the config BEFORE destroy so we can read any cicustom value
	// for snippet cleanup. Once the destroy task completes the VM is
	// gone and GetConfig returns 404.
	var cicustom string
	if cfg, cfgErr := client.GetConfig(ctx, ref.Node, ref.VMID); cfgErr == nil {
		cicustom = cfg["cicustom"]
	}

	if status.Status == "running" {
		label := fmt.Sprintf("Shutting down VM %d", ref.VMID)
		stopFn := client.Shutdown
		if f.hard {
			label = fmt.Sprintf("Stopping VM %d (hard)", ref.VMID)
			stopFn = client.Stop
		}
		if err := runTaskStep(ctx, spinner, label, client, ref.Node, func() (string, error) {
			return stopFn(ctx, ref.Node, ref.VMID)
		}); err != nil {
			return err
		}
	}

	// Clean the snippet BEFORE the irreversible destroy. If the process
	// is interrupted (Ctrl-C, crash) at any point, the snippet is already
	// gone by the time the VM is, so a re-run that hits the "already gone"
	// path never leaves an orphaned pmox-<vmid>-user-data.yaml behind.
	// Cleanup is idempotent (a missing snippet is swallowed), so a re-run
	// before destroy completes is harmless.
	if cicustom != "" {
		if err := snippet.Cleanup(ctx, client, ref.Node, cicustom); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remove snippet for vm %d: %v\n", ref.VMID, err)
		}
	}

	destroyLabel := fmt.Sprintf("Destroying VM %d", ref.VMID)
	if err := runTaskStep(ctx, spinner, destroyLabel, client, ref.Node, func() (string, error) {
		return client.Delete(ctx, ref.Node, ref.VMID)
	}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Deleted VM %q (vmid %d)\n", ref.Name, ref.VMID)
	return nil
}

// stepProgress is the small subset of the launch spinner interface we
// need here. nil is a valid value — runTaskStep no-ops the UI then.
type stepProgress interface {
	Start(label string)
	Done(err error)
}

func runTaskStep(ctx context.Context, p stepProgress, label string, client *pveclient.Client, node string, start func() (string, error)) error {
	if p != nil {
		p.Start(label)
	}
	upid, err := start()
	if err != nil {
		if p != nil {
			p.Done(err)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	if err := client.WaitTask(ctx, node, upid, deleteTaskTimeout); err != nil {
		if p != nil {
			p.Done(err)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	if p != nil {
		p.Done(nil)
	}
	return nil
}

func newDeleteSpinner(stderr io.Writer) stepProgress {
	p := newLaunchProgress(stderr)
	if p == nil {
		return nil
	}
	return p
}
