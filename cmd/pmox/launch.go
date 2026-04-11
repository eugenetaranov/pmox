package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
)

// Built-in defaults applied when neither the CLI flag nor the resolved
// server block supplies a value. Per design D7, these are literals.
const (
	defaultCPU      = 2
	defaultMemMB    = 2048
	defaultDiskSize = "20G"
	defaultWait     = 3 * time.Minute
	defaultUser     = "pmox"
)

// launchFlags holds the raw flag values for a single invocation.
// Using a struct avoids package-level state that would break parallel
// test runs.
type launchFlags struct {
	cpu        int
	memMB      int
	disk       string
	template   string
	storage    string
	node       string
	bridge     string
	user       string
	sshKey     string
	wait       time.Duration
	noWaitSSH  bool
}

func newLaunchCmd() *cobra.Command {
	f := &launchFlags{}
	cmd := &cobra.Command{
		Use:   "launch <name>",
		Short: "Launch a VM from a configured Proxmox template",
		Long: `Launch a new VM on the resolved Proxmox cluster from a cloud-init-
enabled template. Clones the template, tags the new VM, resizes its
disk, pushes built-in cloud-init config, starts it, waits for the
qemu-guest-agent to report an IP, then runs an SSH handshake to
confirm the VM is reachable.

The VM is tagged with 'pmox' immediately after clone so that any
later failure leaves a cleanable VM on the cluster — there is no
automatic rollback. If anything after clone fails, run
'pmox delete <vmid>' to remove it.`,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return fmt.Errorf("missing VM name — usage: pmox launch <name> (example: pmox launch web1)")
			case len(args) > 1:
				return fmt.Errorf("too many arguments: pmox launch takes exactly one VM name, got %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, args[0], f)
		},
	}
	cmd.Flags().IntVar(&f.cpu, "cpu", 0, "number of vCPUs (default 2 if not configured)")
	cmd.Flags().IntVar(&f.memMB, "mem", 0, "memory in MB (default 2048 if not configured)")
	cmd.Flags().StringVar(&f.disk, "disk", "", "disk size (e.g. 20G; default 20G if not configured)")
	cmd.Flags().StringVar(&f.template, "template", "", "template VMID or name (falls back to configured default)")
	cmd.Flags().StringVar(&f.storage, "storage", "", "storage pool for the VM disk (falls back to configured default)")
	cmd.Flags().StringVar(&f.node, "node", "", "cluster node to launch on (falls back to configured default)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge (falls back to configured default)")
	cmd.Flags().StringVar(&f.user, "user", "", "default cloud-init user (default pmox)")
	cmd.Flags().StringVar(&f.sshKey, "ssh-key", "", "path to SSH public key (falls back to configured default)")
	cmd.Flags().DurationVar(&f.wait, "wait", 0, "total wait budget for IP + SSH readiness (default 3m)")
	cmd.Flags().BoolVar(&f.noWaitSSH, "no-wait-ssh", false, "return as soon as an IP is known; skip the SSH handshake")
	return cmd
}

func runLaunch(cmd *cobra.Command, name string, f *launchFlags) error {
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

	// D-T4 verbose log line. Must be emitted before any PVE API call.
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}

	opts, err := resolveLaunchOptions(ctx, name, f, resolved)
	if err != nil {
		return err
	}
	opts.Progress = newLaunchProgress(cmd.ErrOrStderr())

	r, err := launch.Run(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "launched %s (vmid=%d, ip=%s)\n", name, r.VMID, r.IP)
	return nil
}

// resolveLaunchOptions layers flag > configured-default > built-in and
// produces the launch.Options the state machine consumes.
func resolveLaunchOptions(ctx context.Context, name string, f *launchFlags, resolved *server.Resolved) (launch.Options, error) {
	srv := resolved.Server
	client := pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure)

	node := firstNonEmpty(f.node, srv.Node)
	if node == "" {
		return launch.Options{}, fmt.Errorf("%w: no node configured; pass --node or run 'pmox configure'", exitcode.ErrNotFound)
	}

	templateStr := firstNonEmpty(f.template, srv.Template)
	if templateStr == "" {
		return launch.Options{}, fmt.Errorf("%w: no template configured; pass --template or run 'pmox configure'", exitcode.ErrNotFound)
	}
	templateID, templateName, err := resolveTemplate(ctx, client, node, templateStr)
	if err != nil {
		return launch.Options{}, err
	}

	sshKeyPath := firstNonEmpty(f.sshKey, srv.SSHKey)
	if sshKeyPath == "" {
		return launch.Options{}, fmt.Errorf("%w: no ssh key configured; pass --ssh-key or run 'pmox configure'", exitcode.ErrNotFound)
	}
	sshKey, err := readSSHKey(sshKeyPath)
	if err != nil {
		return launch.Options{}, err
	}

	user := firstNonEmpty(f.user, srv.User, defaultUser)

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

	return launch.Options{
		Client:       client,
		Node:         node,
		Name:         name,
		User:         user,
		SSHPubKey:    sshKey,
		TemplateName: templateName,
		TemplateID:   templateID,
		CPU:          cpu,
		MemMB:        mem,
		DiskSize:     disk,
		Storage:      firstNonEmpty(f.storage, srv.Storage),
		Bridge:       firstNonEmpty(f.bridge, srv.Bridge),
		Wait:         wait,
		NoWaitSSH:    f.noWaitSSH,
		Stderr:       os.Stderr,
		Verbose:      verbose,
	}, nil
}

// resolveTemplate accepts either a VMID (all digits) or a template
// name. For names, it queries ListTemplates on the resolved node and
// matches by Template.Name.
func resolveTemplate(ctx context.Context, client *pveclient.Client, node, raw string) (int, string, error) {
	if id, err := strconv.Atoi(raw); err == nil {
		return id, "", nil
	}
	tmpls, _, err := client.ListTemplates(ctx, node)
	if err != nil {
		return 0, "", fmt.Errorf("list templates on %s: %w", node, err)
	}
	for _, t := range tmpls {
		if t.Name == raw {
			return t.VMID, t.Name, nil
		}
	}
	return 0, "", fmt.Errorf("%w: template %q not found on node %s", pveclient.ErrNotFound, raw, node)
}

// readSSHKey resolves a path (expanding ~) and returns the file
// contents trimmed of leading/trailing whitespace.
func readSSHKey(path string) (string, error) {
	expanded := path
	if strings.HasPrefix(expanded, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			expanded = filepath.Join(home, expanded[2:])
		}
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return "", fmt.Errorf("read ssh key %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
