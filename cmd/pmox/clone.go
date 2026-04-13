package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func newCloneCmd() *cobra.Command {
	f := &launchFlags{}
	cmd := &cobra.Command{
		Use:   "clone <source-name|vmid> <new-name>",
		Short: "Clone an existing VM into a new VM",
		Long: `Clone an existing VM (template or regular VM) into a new VM. This is
conceptually 'pmox launch', except the template is the resolved
source VM instead of the configured template.

The same --cpu/--mem/--disk/--wait/--no-wait-ssh flags are accepted
and are applied to the clone. Flags unset on the command line fall
back to the configured defaults, same as launch.

Cloud-init user-data comes from
~/.config/pmox/cloud-init/<host>-<port>.yaml, which 'pmox configure'
writes on first run. Edit that file to customize the new VM, or run
'pmox configure --regen-cloud-init' to rewrite it.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(cmd, args[0], args[1], f)
		},
	}
	cmd.Flags().IntVar(&f.cpu, "cpu", 0, "number of vCPUs (default 2 if not configured)")
	cmd.Flags().IntVar(&f.memMB, "mem", 0, "memory in MB (default 2048 if not configured)")
	cmd.Flags().StringVar(&f.disk, "disk", "", "disk size (e.g. 20G; default 20G if not configured)")
	cmd.Flags().StringVar(&f.storage, "storage", "", "storage pool for the VM disk (falls back to configured default)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge (falls back to configured default)")
	cmd.Flags().DurationVar(&f.wait, "wait", 0, "total wait budget for IP + SSH readiness (default 3m)")
	cmd.Flags().BoolVar(&f.noWaitSSH, "no-wait-ssh", false, "return as soon as an IP is known; skip the SSH handshake")
	return cmd
}

func runClone(cmd *cobra.Command, srcArg, newName string, f *launchFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.Load()
	if err != nil {
		return err
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
		return err
	}
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}
	srv := resolved.Server
	client := pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure)

	cloudInitPath, err := config.CloudInitPath(resolved.URL)
	if err != nil {
		return fmt.Errorf("resolve cloud-init path: %w", err)
	}
	cpu := f.cpu
	if cpu == 0 {
		cpu = defaultCPU
	}
	mem := f.memMB
	if mem == 0 {
		mem = defaultMemMB
	}
	disk := firstNonEmpty(f.disk, defaultDiskSize)
	wait := f.wait
	if wait == 0 {
		wait = defaultWait
	}

	partial := launch.Options{
		CPU:           cpu,
		MemMB:         mem,
		DiskSize:      disk,
		Storage:       firstNonEmpty(f.storage, srv.Storage),
		Bridge:        firstNonEmpty(f.bridge, srv.Bridge),
		Wait:          wait,
		NoWaitSSH:     f.noWaitSSH,
		CloudInitPath: cloudInitPath,
		Stderr:        os.Stderr,
		Verbose:       verbose,
		Progress:      newLaunchProgress(cmd.ErrOrStderr()),
	}
	return executeClone(ctx, cmd, client, srcArg, newName, partial)
}

// executeClone is the testable half: given a resolved client and a
// pre-populated launch.Options (with Client/Node/Name/Template* left
// blank), it resolves the source VM and drives launch.Run.
func executeClone(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, srcArg, newName string, partial launch.Options) error {
	ref, err := vm.Resolve(ctx, client, srcArg)
	if err != nil {
		return err
	}
	partial.Client = client
	partial.Node = ref.Node
	partial.Name = newName
	partial.TemplateID = ref.VMID
	partial.TemplateName = ref.Name
	r, err := launch.Run(ctx, partial)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "cloned %s -> %s (vmid=%d, ip=%s)\n", ref.Name, newName, r.VMID, r.IP)
	return nil
}
