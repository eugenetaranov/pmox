package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func newCloneCmd() *cobra.Command {
	f := &launchFlags{}
	cmd := &cobra.Command{
		Use:   "clone [source-name|vmid] <new-name>",
		Short: "Clone an existing VM into a new VM",
		Long: `Clone an existing VM (template or regular VM) into a new VM. This is
conceptually 'pmox launch', except the template is the resolved
source VM instead of the configured template.

The same --cpu/--mem/--disk/--wait/--no-wait-ssh flags are accepted
and are applied to the clone. Flags unset on the command line fall
back to the configured defaults, same as launch.

Cloud-init user-data comes from
~/.config/pmox/cloud-init/<host>-<port>.yaml, which 'pmox init'
writes on first run. Edit that file to customize the new VM, or run
'pmox init --regen-cloud-init' to rewrite it.

--storage and --snippet-storage are independent: the first targets
the new VM's disk, the second targets the cloud-init snippet upload
(must support 'snippets'). --snippet-storage falls back to the
configured snippet_storage, then to --storage with a warning.

On a terminal, 'pmox clone' alone prompts for the source VM (the
shared picker) and the new name; 'pmox clone web1' skips straight to
the new-name prompt.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var src, newName string
			switch len(args) {
			case 1:
				newName = args[0]
			case 2:
				src, newName = args[0], args[1]
			}
			return runClone(cmd, src, newName, f)
		},
	}
	cmd.Flags().IntVar(&f.cpu, "cpu", 0, "number of vCPU cores (default 2)")
	cmd.Flags().IntVar(&f.memGB, "mem", 0, "memory in GiB (default 2)")
	cmd.Flags().IntVar(&f.diskGB, "disk", 0, "disk size in GiB (default 20)")
	cmd.Flags().StringVar(&f.storage, "storage", "", "storage pool for the VM disk (falls back to configured default)")
	cmd.Flags().StringVar(&f.snippetStorage, "snippet-storage", "", "storage pool for the cloud-init snippet (falls back to configured snippet_storage, then storage)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge for the clone's net0 (default: keep the source VM's bridge)")
	cmd.Flags().DurationVar(&f.wait, "wait", 0, "total wait budget for IP + SSH readiness (default 3m)")
	cmd.Flags().BoolVar(&f.noWaitSSH, "no-wait-ssh", false, "return as soon as an IP is known; skip the SSH handshake")
	addHookFlags(cmd, f)
	return cmd
}

func runClone(cmd *cobra.Command, srcArg, newName string, f *launchFlags) error {
	ctx := cmd.Context()
	f.bridgeSet = cmd.Flags().Changed("bridge")
	// Resolve hook flags first so mutual-exclusion errors short-circuit
	// before any config load / server resolution / PVE call.
	hk, err := resolveHook(f)
	if err != nil {
		return err
	}
	// No new name given → same terminal-only prompt launch uses for its
	// VM name, checked before any config load / server resolution / PVE
	// call; non-interactively this is still a hard, immediate error.
	if newName == "" {
		if !tui.Interactive() || outputMode == "json" {
			return fmt.Errorf("%w: missing new VM name — usage: pmox clone [source-name|vmid] <new-name> (example: pmox clone web1 web2)", exitcode.ErrUserInput)
		}
		if newName, err = promptRequired(newStdPrompter(ctx), "New VM name: ", "a new VM name is required"); err != nil {
			return err
		}
	}
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	if err := resolved.RequireNodeSSH("clone"); err != nil {
		return err
	}
	// Resolve resources before any picker prompt so a missing storage
	// fails fast instead of reaching PVE as ide2=":cloudinit".
	partial, err := resolveVMSpec(f, resolved, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// No source given → pick one interactively (like shell/delete do).
	if srcArg == "" {
		picked, err := vmPickFn(ctx, client)
		if err != nil {
			return err
		}
		srcArg = strconv.Itoa(picked.VMID)
	}

	upload, closeUpload := newSnippetUploader(resolved)
	defer closeUpload()

	partial.UploadSnippet = upload
	partial.Progress = newLaunchProgress(cmd.ErrOrStderr())
	applyHookOptions(&partial, hk, f, resolved.Server, resolved.URL, SSHInsecure())
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
	r, err := launch.Run(ctx, partial)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "cloned %s -> %s (vmid=%d, ip=%s)\n", ref.Name, newName, r.VMID, r.IP)
	return nil
}
