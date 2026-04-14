package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/hook"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
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
	cpu            int
	memMB          int
	disk           string
	template       string
	storage        string
	snippetStorage string
	node           string
	bridge         string
	wait           time.Duration
	noWaitSSH      bool
	postCreate     string
	tack           string
	ansible        string
	strictHooks    bool
}

func newLaunchCmd() *cobra.Command {
	f := &launchFlags{}
	cmd := &cobra.Command{
		Use:   "launch <name>",
		Short: "Launch a VM from a configured Proxmox template",
		Long: `Launch a new VM on the resolved Proxmox cluster from a cloud-init-
enabled template. Clones the template, tags the new VM, resizes its
disk, uploads the per-server cloud-init snippet, starts the VM, waits
for the qemu-guest-agent to report an IP, then runs an SSH handshake
to confirm the VM is reachable.

The cloud-init user-data is read from
~/.config/pmox/cloud-init/<host>-<port>.yaml, which 'pmox configure'
writes on first run. Edit that file to customize packages, users,
runcmd, or anything else cloud-init supports. To regenerate a fresh
default, run 'pmox configure --regen-cloud-init'.

The VM disk and the cloud-init snippet may live on different storage
pools. --storage targets the disk; --snippet-storage targets the
snippet (which must support the 'snippets' content type). Either
falls back to the matching configured default; --snippet-storage
additionally falls back to --storage with a one-shot warning when
no snippet_storage is configured.

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
	cmd.Flags().StringVar(&f.snippetStorage, "snippet-storage", "", "storage pool for the cloud-init snippet (falls back to configured snippet_storage, then storage)")
	cmd.Flags().StringVar(&f.node, "node", "", "cluster node to launch on (falls back to configured default)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge (falls back to configured default)")
	cmd.Flags().DurationVar(&f.wait, "wait", 0, "total wait budget for IP + SSH readiness (default 3m)")
	cmd.Flags().BoolVar(&f.noWaitSSH, "no-wait-ssh", false, "return as soon as an IP is known; skip the SSH handshake")
	addHookFlags(cmd, f)
	return cmd
}

// addHookFlags registers the post-create hook flags on a launch-style
// command. Shared between launch and clone so their hook surface stays
// identical.
func addHookFlags(cmd *cobra.Command, f *launchFlags) {
	cmd.Flags().StringVar(&f.postCreate, "post-create", "", "path to a script to run after SSH is ready; receives PMOX_IP, PMOX_VMID, PMOX_NAME, PMOX_USER, PMOX_NODE env vars")
	cmd.Flags().StringVar(&f.tack, "tack", "", "path to a tack config; runs tack apply against the new VM after SSH is ready")
	cmd.Flags().StringVar(&f.ansible, "ansible", "", "path to an Ansible playbook; runs ansible-playbook against the new VM after SSH is ready")
	cmd.Flags().BoolVar(&f.strictHooks, "strict-hooks", false, "treat hook failure as fatal (exit ExitHook) instead of a stderr warning")
}

// resolveHook enforces mutual exclusion among --post-create, --tack,
// and --ansible and returns the chosen hook implementation (or nil).
// The error returned when multiple flags are set wraps ErrUserInput so
// the top-level exit-code mapping produces ExitUserError.
func resolveHook(f *launchFlags) (hook.Hook, error) {
	var set []string
	if f.postCreate != "" {
		set = append(set, "--post-create")
	}
	if f.tack != "" {
		set = append(set, "--tack")
	}
	if f.ansible != "" {
		set = append(set, "--ansible")
	}
	if len(set) > 1 {
		return nil, fmt.Errorf("%w: --post-create, --tack, and --ansible are mutually exclusive; pick one of %s", exitcode.ErrUserInput, strings.Join(set, ", "))
	}
	switch {
	case f.postCreate != "":
		return &hook.PostCreateHook{Path: f.postCreate}, nil
	case f.tack != "":
		return &hook.TackHook{ConfigPath: f.tack}, nil
	case f.ansible != "":
		return &hook.AnsibleHook{PlaybookPath: f.ansible}, nil
	}
	return nil, nil
}

// hookSSHDefaults resolves the SSH user and private-key path exposed to
// hooks via Env.User / Env.SSHKey. Honors server.User (fallback to the
// built-in defaultUser) and derives the private key from the configured
// ssh_pubkey by stripping `.pub`.
func hookSSHDefaults(srv *config.Server) (user, sshKey string) {
	user = firstNonEmpty(srv.User, defaultUser)
	if srv.SSHPubkey != "" {
		sshKey = expandHome(strings.TrimSuffix(srv.SSHPubkey, ".pub"))
	}
	return user, sshKey
}


func runLaunch(cmd *cobra.Command, name string, f *launchFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Resolve hook flags before any config load / server resolution /
	// PVE call so --post-create + --tack (etc.) fail immediately with
	// zero network traffic.
	hk, err := resolveHook(f)
	if err != nil {
		return err
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

	if !resolved.HasNodeSSH() {
		return fmt.Errorf("%w: launch needs SSH access to the Proxmox node (for cloud-init snippet upload). Run 'pmox configure' to add SSH credentials", exitcode.ErrUserInput)
	}

	opts, err := resolveLaunchOptions(ctx, name, f, resolved, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	opts.Hook = hk
	opts.StrictHooks = f.strictHooks
	opts.User, opts.SSHKeyPath = hookSSHDefaults(resolved.Server)
	opts.Progress = newLaunchProgress(cmd.ErrOrStderr())

	upload, closeUpload := newSnippetUploader(resolved)
	defer closeUpload()
	opts.UploadSnippet = upload

	r, err := launch.Run(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "launched %s (vmid=%d, ip=%s)\n", name, r.VMID, r.IP)
	return nil
}

// newSnippetUploader returns a closure that lazily dials pvessh on
// first use and writes a snippet via SFTP, plus a cleanup func that
// closes the underlying SSH session if it was opened. Lazy so the
// SSH handshake cost (and its failure modes) are only paid when the
// upload phase actually runs.
func newSnippetUploader(resolved *server.Resolved) (func(ctx context.Context, storagePath, filename string, content []byte) error, func()) {
	var sshClient *pvessh.Client
	upload := func(ctx context.Context, storagePath, filename string, content []byte) error {
		if sshClient == nil {
			c, err := dialPvessh(ctx, resolved)
			if err != nil {
				return fmt.Errorf("ssh to %s: %w", resolved.URL, err)
			}
			sshClient = c
		}
		return sshClient.UploadSnippet(ctx, storagePath, filename, content)
	}
	cleanup := func() {
		if sshClient != nil {
			_ = sshClient.Close()
		}
	}
	return upload, cleanup
}

// resolveLaunchOptions layers flag > configured-default > built-in and
// produces the launch.Options the state machine consumes.
func resolveLaunchOptions(ctx context.Context, name string, f *launchFlags, resolved *server.Resolved, stderr io.Writer) (launch.Options, error) {
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

	storage := firstNonEmpty(f.storage, srv.Storage)
	if storage == "" {
		return launch.Options{}, fmt.Errorf("%w: no storage configured; pass --storage or run 'pmox configure' (required for the cloud-init drive)", exitcode.ErrNotFound)
	}
	snippetStorage := resolveSnippetStorage(f.snippetStorage, srv.SnippetStorage, storage, stderr)

	cloudInitPath, err := config.CloudInitPath(resolved.URL)
	if err != nil {
		return launch.Options{}, fmt.Errorf("resolve cloud-init path: %w", err)
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

	return launch.Options{
		Client:         client,
		Node:           node,
		Name:           name,
		TemplateName:   templateName,
		TemplateID:     templateID,
		CPU:            cpu,
		MemMB:          mem,
		DiskSize:       disk,
		Storage:        storage,
		SnippetStorage: snippetStorage,
		Bridge:         firstNonEmpty(f.bridge, srv.Bridge),
		Wait:           wait,
		NoWaitSSH:      f.noWaitSSH,
		CloudInitPath:  cloudInitPath,
		Stderr:         stderr,
		Verbose:        verbose,
	}, nil
}

// resolveSnippetStorage layers --snippet-storage > server.SnippetStorage
// > VM disk storage. When the final fallback to disk storage kicks in
// (no flag, no config), it emits a one-shot stderr warning pointing the
// user at `pmox configure` for a permanent fix.
func resolveSnippetStorage(flag, configured, diskStorage string, stderr io.Writer) string {
	if flag != "" {
		return flag
	}
	if configured != "" {
		return configured
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "warning: no snippet_storage configured; falling back to %q. run 'pmox configure' to set it permanently\n", diskStorage)
	}
	return diskStorage
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
