package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const startTaskTimeout = 120 * time.Second

type startFlags struct {
	noWait bool
	wait   time.Duration
}

func newStartCmd() *cobra.Command {
	f := &startFlags{}
	cmd := &cobra.Command{
		Use:   "start <name|vmid>",
		Short: "Start a stopped VM",
		Long: `Start a VM on the resolved Proxmox cluster. By default, pmox waits
for the start task to complete and then polls the qemu-guest-agent
until it reports a usable IPv4 address, mirroring 'pmox launch'.

--no-wait returns as soon as the start task finishes and skips the
IP-wait loop. --wait overrides the default 3m budget for the IP poll.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, args[0], f)
		},
	}
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return after the start task completes; skip the IP-ready poll")
	cmd.Flags().DurationVar(&f.wait, "wait", defaultWait, "total wait budget for the guest agent to report an IP")
	return cmd
}

func runStart(cmd *cobra.Command, arg string, f *startFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := buildDeleteClient(ctx, cmd)
	if err != nil {
		return err
	}
	return executeStart(ctx, cmd, client, arg, f)
}

func executeStart(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *startFlags) error {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}
	upid, err := client.Start(ctx, ref.Node, ref.VMID)
	if err != nil {
		return fmt.Errorf("start vm %d: %w", ref.VMID, err)
	}
	if err := client.WaitTask(ctx, ref.Node, upid, startTaskTimeout); err != nil {
		return fmt.Errorf("start vm %d: %w", ref.VMID, err)
	}
	if f.noWait {
		fmt.Fprintf(cmd.OutOrStdout(), "started %s (vmid=%d)\n", ref.Name, ref.VMID)
		return nil
	}
	ip, err := launch.WaitForIP(ctx, client, ref.Node, ref.VMID, f.wait)
	if err != nil {
		return fmt.Errorf("wait for ip on vm %d: %w", ref.VMID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "started %s (vmid=%d, ip=%s)\n", ref.Name, ref.VMID, ip)
	return nil
}
