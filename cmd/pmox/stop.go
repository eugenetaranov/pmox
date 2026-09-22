package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const stopTaskTimeout = 120 * time.Second

type stopFlags struct {
	force  bool
	noWait bool
}

func newStopCmd() *cobra.Command {
	f := &stopFlags{}
	cmd := &cobra.Command{
		Use:   "stop [name|vmid ...]",
		Short: "Gracefully shut down VMs (or hard-stop with --force)",
		Long: `Stop one or more VMs on the resolved Proxmox cluster. Default is ACPI
graceful shutdown via POST /status/shutdown. --force sends a hard
power-off via /status/stop — use it when the guest is unresponsive.

If no arguments are given, pmox auto-selects the only pmox VM when one
exists, or shows a multi-select picker when there are several.

--no-wait returns as soon as the stop task is queued; otherwise
pmox waits for the PVE task to complete.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.force, "force", false, "hard power-off instead of ACPI graceful shutdown")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return after the stop task is queued instead of waiting for completion")
	return cmd
}

func runStop(cmd *cobra.Command, args []string, f *stopFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := buildDeleteClient(ctx, cmd)
	if err != nil {
		return err
	}
	targets, err := resolveTargetArgs(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	var failed int
	for _, t := range targets {
		if err := executeStop(ctx, cmd, client, t, f); err != nil {
			if len(targets) == 1 {
				return err
			}
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "stop %q failed: %v\n", t, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d VMs failed to stop", failed, len(targets))
	}
	return nil
}

func executeStop(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *stopFlags) error {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}
	var (
		upid  string
		label string
	)
	if f.force {
		label = "stop"
		upid, err = client.Stop(ctx, ref.Node, ref.VMID)
	} else {
		label = "shutdown"
		upid, err = client.Shutdown(ctx, ref.Node, ref.VMID)
	}
	if err != nil {
		return fmt.Errorf("%s vm %d: %w", label, ref.VMID, err)
	}
	if !f.noWait {
		if err := client.WaitTask(ctx, ref.Node, upid, stopTaskTimeout); err != nil {
			return fmt.Errorf("%s vm %d: %w", label, ref.VMID, err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s (vmid=%d)\n", label, ref.Name, ref.VMID)
	return nil
}
