package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// The default VM (openspec/specs/default-vm): 'pmox vm use <name>' saves
// it with the context; shell, exec and the other everyday commands then
// go to it when no VM is named. Commands that change or remove a VM
// never use it.

// sessionDefault is the default VM of the server this process connected
// to, set by connect.
var sessionDefault struct {
	url  string // canonical server URL
	name string // "" when no default is set
}

// pickFlag is --pick: ignore the default VM for this run.
var pickFlag bool

// setSessionDefault records srv's default VM for this process and tells
// the pickers which row to mark.
func setSessionDefault(url string, srv *config.Server) {
	sessionDefault.url, sessionDefault.name = url, ""
	if srv != nil {
		sessionDefault.name = srv.DefaultVM
	}
	vm.DefaultName = sessionDefault.name
}

// addPickFlag registers --pick on a command that uses the default VM.
func addPickFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&pickFlag, "pick", false, "choose the VM from the picker, ignoring the default VM (see 'pmox vm use')")
}

// saveDefaultVMFn stores (or, with "", clears) the default VM. A seam
// for tests.
var saveDefaultVMFn = config.SetDefaultVM

// pickTargetVM resolves a missing VM argument for a command that uses
// the default VM: the default when one is set, it still exists and
// --pick wasn't given; else the usual rules (the only VM, a picker, or
// the missing-argument error). A default that is gone is reported and
// cleared; an unreachable cluster clears nothing.
func pickTargetVM(ctx context.Context, client *pveclient.Client, stderr io.Writer) (*vm.Ref, error) {
	if def := sessionDefault.name; def != "" && !pickFlag {
		ref, err := vm.Resolve(ctx, client, def)
		switch {
		case err == nil && vm.HasPMOXTag(ref.Tags):
			fmt.Fprintf(stderr, "→ %s (default VM)\n", ref.Name)
			return ref, nil
		case err == nil || errors.Is(err, pveclient.ErrNotFound):
			fmt.Fprintf(stderr, "default VM %q no longer exists — cleared (set another with 'pmox vm use')\n", def)
			clearSessionDefault(stderr)
		default:
			return nil, err
		}
	}
	return vmPickFn(ctx, client)
}

// clearSessionDefault removes the stored default VM. A failed write is
// reported and otherwise ignored: the command at hand can still run.
func clearSessionDefault(stderr io.Writer) {
	if err := saveDefaultVMFn(sessionDefault.url, ""); err != nil {
		fmt.Fprintf(stderr, "warning: couldn't clear the default VM: %v\n", err)
	}
	sessionDefault.name, vm.DefaultName = "", ""
}

// forgetDefaultVM clears the default VM when ref, just deleted, was it.
func forgetDefaultVM(cmd *cobra.Command, ref *vm.Ref) {
	if sessionDefault.name == "" || ref.Name != sessionDefault.name {
		return
	}
	clearSessionDefault(cmd.ErrOrStderr())
	fmt.Fprintf(cmd.ErrOrStderr(), "%s was the default VM — cleared\n", ref.Name)
}

// resolveDefaultTargetArg is resolveTargetArg for the commands that use
// the default VM.
func resolveDefaultTargetArg(ctx context.Context, client *pveclient.Client, args []string, stderr io.Writer) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	ref, err := pickTargetVM(ctx, client, stderr)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(ref.VMID), nil
}

// targetDefaultVM is the VM the interactive <vm>:<path> field opens
// with ("" for none, or with --pick).
func targetDefaultVM() string {
	if pickFlag {
		return ""
	}
	return sessionDefault.name
}

func newVMUseCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use [name|vmid]",
		Short: "Set the default VM",
		Long: `Set the default VM of the current context. With no VM named, shell,
exec, vm info, start, apply, vm ssh-config, yardo push, yardo gitkey and
the <vm>:<path> targets of cp, sync and mount then go to it instead of
showing the picker; --pick on those commands shows the picker once.

stop, delete, resize and clone never use the default: they keep asking.

Without an argument, pick the VM (on a terminal). --clear removes the
default.`,
		Example: `  pmox vm use web1
  pmox vm use            # pick one
  pmox vm use --clear`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear && len(args) > 0 {
				return fmt.Errorf("%w: --clear takes no VM", exitcode.ErrUserInput)
			}
			return runVMUse(cmd, args, clear)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the default VM")
	return cmd
}

func runVMUse(cmd *cobra.Command, args []string, clear bool) error {
	ctx := cmd.Context()
	s, err := connect(ctx, cmd, connectOptions{Stdin: os.Stdin})
	if err != nil {
		return err
	}
	url := s.Resolved.URL
	label := s.Cfg.ContextName(url)
	if clear {
		if err := saveDefaultVMFn(url, ""); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ no default VM for %s\n", label)
		return nil
	}
	arg, err := resolveTargetArg(ctx, s.Client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	ref, err := vm.Resolve(ctx, s.Client, arg)
	if err != nil {
		return err
	}
	if err := ref.RequirePMOXTag("use as the default", false); err != nil {
		return err
	}
	if err := saveDefaultVMFn(url, ref.Name); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ default VM for %s: %s\n", label, ref.Name)
	return nil
}
