package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// deleteTaskTimeout bounds each underlying PVE task (shutdown/stop,
// destroy). Matches the launch command's 120s clone/WaitTask budget.
const deleteTaskTimeout = 120 * time.Second

type deleteFlags struct {
	force bool
	yes   bool
}

func newDeleteCmd() *cobra.Command {
	f := &deleteFlags{}
	cmd := &cobra.Command{
		Use:   "delete <name|vmid>",
		Short: "Stop and destroy a pmox-launched VM",
		Long: `Delete a VM on the resolved Proxmox cluster. The argument may be
either the VM name (e.g. "web1") or its numeric VMID (e.g. "104").

Before issuing any destructive API call the command prints a summary of
the resolved VM and requires an interactive y/N confirmation (default No).
Pass --yes / -y or set PMOX_ASSUME_YES=1 to skip the prompt for scripted
and CI use. When stdin is not a TTY and no bypass is set, the command
refuses to proceed.

By default, delete refuses to act on VMs that are not tagged "pmox".
Since pmox launch tags every VM it creates, this rule means delete
will only touch VMs pmox launched — hand-managed VMs are protected
from accidental destruction.

--force relaxes two things at once: (1) it bypasses the tag check,
allowing delete on untagged VMs, and (2) it uses hard "stop" (power
off) instead of graceful "shutdown" (ACPI). Reach for --force when
the VM is hand-managed or when the guest is not responding to ACPI.
--force is orthogonal to --yes: using --force alone still prompts.

If the VM has already been destroyed, delete exits 0 with a note on
stderr so scripted loops are idempotent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDelete(cmd, args[0], f)
		},
	}
	cmd.Flags().BoolVar(&f.force, "force", false, "bypass the pmox tag check and use hard stop instead of graceful shutdown")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "skip the confirmation prompt (env: PMOX_ASSUME_YES)")
	return cmd
}

func runDelete(cmd *cobra.Command, arg string, f *deleteFlags) error {
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
		return fmt.Errorf("refusing to delete VM %q: stdin is not a TTY and --yes was not passed; re-run with --yes (or PMOX_ASSUME_YES=1) for non-interactive use", arg)
	}

	client, err := buildDeleteClient(ctx, cmd)
	if err != nil {
		return err
	}
	return executeDelete(ctx, cmd, client, arg, f, confirmer)
}

func buildDeleteClient(ctx context.Context, cmd *cobra.Command) (*pveclient.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:    cfg,
		Flag:   serverFlag,
		Env:    os.Getenv("PMOX_SERVER"),
		Stdin:  os.Stdin,
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	})
	if err != nil {
		return nil, err
	}
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}
	srv := resolved.Server
	return pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure), nil
}

// executeDelete holds the command logic without server/config wiring
// so tests can drive it with a fake client directly.
func executeDelete(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *deleteFlags, confirmer tui.Confirmer) error {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}

	if !f.force && !vm.HasPMOXTag(ref.Tags) {
		return fmt.Errorf("refusing to delete VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)
	}

	if !f.yes {
		tags := ref.Tags
		if tags == "" {
			tags = "<none>"
		}
		var prompt string
		if f.force {
			prompt = fmt.Sprintf("About to FORCE-delete VM %q (vmid %d, node %s, tags %s)\nThis will use hard stop (no graceful shutdown) and bypasses the pmox tag check.\nContinue? [y/N]: ", ref.Name, ref.VMID, ref.Node, tags)
		} else {
			prompt = fmt.Sprintf("About to delete VM %q (vmid %d, node %s, tags %s)\nContinue? [y/N]: ", ref.Name, ref.VMID, ref.Node, tags)
		}
		ok, err := confirmer.Confirm(ctx, prompt)
		if err != nil {
			return fmt.Errorf("confirmation: %w", err)
		}
		if !ok {
			return fmt.Errorf("delete cancelled")
		}
	}

	status, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "VM %q (vmid %d) is already gone\n", ref.Name, ref.VMID)
			return nil
		}
		return fmt.Errorf("get status for vm %d: %w", ref.VMID, err)
	}

	spinner := newDeleteSpinner(cmd.ErrOrStderr())

	if status.Status == "running" {
		label := fmt.Sprintf("Shutting down VM %d", ref.VMID)
		stopFn := client.Shutdown
		if f.force {
			label = fmt.Sprintf("Stopping VM %d (force)", ref.VMID)
			stopFn = client.Stop
		}
		if err := runTaskStep(ctx, spinner, label, client, ref.Node, func() (string, error) {
			return stopFn(ctx, ref.Node, ref.VMID)
		}); err != nil {
			return err
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
