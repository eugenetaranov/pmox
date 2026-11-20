package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

type restartFlags struct {
	hard   bool
	noWait bool
	wait   time.Duration
}

func newRestartCmd() *cobra.Command {
	f := &restartFlags{}
	cmd := &cobra.Command{
		Use:   "restart [name|vmid ...]",
		Short: "Stop and start VMs",
		Long: `Stop one or more VMs and start them again: a full stop and start, so
pending hardware changes (pmox resize) take effect, which a reboot from
inside the VM doesn't do. A VM that is already stopped is just started.

The stop is a graceful shutdown; --hard powers the VM off instead, for
a guest that doesn't respond. pmox then waits, as 'pmox start' does,
for the VM to report an IP (--no-wait skips that, --wait sets the
budget).

With no VM named, pmox picks the only pmox VM, or shows a multi-select
picker when there are several. The default VM is not used.`,
		Example: `  pmox restart web1
  pmox restart web1 web2
  pmox restart --hard web1`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.hard, "hard", false, "hard power-off instead of a graceful shutdown")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return after the start task completes; skip the IP-ready poll")
	cmd.Flags().DurationVar(&f.wait, "wait", defaultWait, "total wait budget for the guest agent to report an IP")
	return cmd
}

func runRestart(cmd *cobra.Command, args []string, f *restartFlags) error {
	ctx := cmd.Context()
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	targets, err := resolveTargetArgs(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	var failed int
	for _, t := range targets {
		if err := executeRestart(ctx, cmd, client, t, f); err != nil {
			if len(targets) == 1 {
				return err
			}
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "restart %q failed: %v\n", t, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d VMs failed to restart", failed, len(targets))
	}
	return nil
}

// executeRestart stops arg (waiting for it to be down) and starts it
// again. A VM left running by a shutdown that didn't finish is not
// touched further.
func executeRestart(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *restartFlags) error {
	if err := executeStop(ctx, cmd, client, arg, &stopFlags{force: f.hard}); err != nil {
		if !f.hard {
			return fmt.Errorf("%w — the VM was left as it is; 'pmox restart --hard %s' powers it off instead", err, arg)
		}
		return err
	}
	return executeStart(ctx, cmd, client, arg, &startFlags{noWait: f.noWait, wait: f.wait})
}
