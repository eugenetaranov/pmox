package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

const startTaskTimeout = 120 * time.Second

type startFlags struct {
	noWait bool
	wait   time.Duration
}

func newStartCmd() *cobra.Command {
	f := &startFlags{}
	cmd := &cobra.Command{
		Use:   "start [name|vmid]",
		Short: "Start a stopped VM",
		Long: `Start a VM on the resolved Proxmox cluster. By default, pmox waits
for the start task to complete and then polls the qemu-guest-agent
until it reports a usable IPv4 address, mirroring 'pmox launch'.

If the argument is omitted, pmox auto-selects the only pmox VM when
one exists, or shows an interactive picker when there are several.

--no-wait returns as soon as the start task finishes and skips the
IP-wait loop. --wait overrides the default 3m budget for the IP poll.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return after the start task completes; skip the IP-ready poll")
	cmd.Flags().DurationVar(&f.wait, "wait", defaultWait, "total wait budget for the guest agent to report an IP")
	return cmd
}

func runStart(cmd *cobra.Command, args []string, f *startFlags) error {
	ctx := cmd.Context()
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	arg, err := resolveTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return executeStart(ctx, cmd, client, arg, f)
}

func executeStart(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *startFlags) error {
	sp := startSpin(fmt.Sprintf("Starting %s…", arg))
	defer sp.Stop()
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}
	sp.Set(fmt.Sprintf("Starting %s…", ref.Name))
	// Best effort: an unreadable status falls through to the start task.
	already := false
	if st, err := client.GetStatus(ctx, ref.Node, ref.VMID); err == nil && st.IsRunning() {
		already = true
	}
	if !already {
		upid, err := client.Start(ctx, ref.Node, ref.VMID)
		if err == nil {
			err = client.WaitTask(ctx, ref.Node, upid, startTaskTimeout)
		}
		switch {
		case isAlreadyErr(err, "already running"):
			already = true
		case err != nil:
			return fmt.Errorf("start vm %d: %w", ref.VMID, err)
		}
	}
	verb := "started"
	if already {
		verb = "is already running"
	}
	if f.noWait {
		finishSpin(cmd, sp, fmt.Sprintf("%s %s (vmid %d)", ref.Name, verb, ref.VMID))
		return nil
	}
	sp.Set(fmt.Sprintf("Waiting for %s to get an IP…", ref.Name))
	ip, err := vmwait.WaitForIP(ctx, client, ref.Node, ref.VMID, f.wait)
	if err != nil {
		return fmt.Errorf("wait for ip on vm %d: %w", ref.VMID, err)
	}
	finishSpin(cmd, sp, fmt.Sprintf("%s %s (vmid %d, ip %s)", ref.Name, verb, ref.VMID, ip))
	return nil
}

// isAlreadyErr reports whether err is Proxmox saying the VM is already in
// the requested state (a race with our own status check).
func isAlreadyErr(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
