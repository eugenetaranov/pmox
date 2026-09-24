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
	"github.com/eugenetaranov/pmox/internal/sshkey"
)

// Built-in defaults applied when neither the CLI flag nor the resolved
// server block supplies a value. Per design D7, these are literals.
const (
	defaultCPU    = 2
	defaultMemGB  = 2
	defaultDiskGB = 20
	defaultWait   = 3 * time.Minute
	defaultUser   = "pmox"
	// tackDefaultSentinel is the value cobra assigns to --tack when the
	// flag is given without an argument; it maps to the config playbook.
	tackDefaultSentinel = "\x00pmox-default-playbook"
)

// launchFlags holds the raw flag values for a single invocation.
// Using a struct avoids package-level state that would break parallel
// test runs.
type launchFlags struct {
	cpu            int
	memGB          int
	diskGB         int
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
	// bridgeSet is true when --bridge was passed explicitly. Only then is
	// the new VM's net0 rewritten; the configured default bridge is for
	// create-template, and a launched/cloned VM keeps its source's NIC.
	bridgeSet bool
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
~/.config/pmox/cloud-init/<host>-<port>.yaml, which 'pmox init'
writes on first run. Edit that file to customize packages, users,
runcmd, or anything else cloud-init supports. To regenerate a fresh
default, run 'pmox init --regen-cloud-init'.

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
	cmd.Flags().IntVar(&f.cpu, "cpu", 0, "number of vCPU cores (default 2)")
	cmd.Flags().IntVar(&f.memGB, "mem", 0, "memory in GiB (default 2)")
	cmd.Flags().IntVar(&f.diskGB, "disk", 0, "disk size in GiB (default 20)")
	cmd.Flags().StringVar(&f.template, "template", "", "template VMID or name (falls back to configured default)")
	cmd.Flags().StringVar(&f.storage, "storage", "", "storage pool for the VM disk (falls back to configured default)")
	cmd.Flags().StringVar(&f.snippetStorage, "snippet-storage", "", "storage pool for the cloud-init snippet (falls back to configured snippet_storage, then storage)")
	cmd.Flags().StringVar(&f.node, "node", "", "cluster node to launch on (falls back to configured default)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge for the VM's net0 (default: keep the template's bridge)")
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
	cmd.Flags().StringVar(&f.tack, "tack", "", "path to a tack playbook; runs 'tack run' against the new VM after SSH is ready (omit the value to use ~/.config/pmox/tack/playbook.yaml)")
	// Allow `--tack` with no value → default config playbook.
	cmd.Flags().Lookup("tack").NoOptDefVal = tackDefaultSentinel
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
		path := f.tack
		if path == tackDefaultSentinel {
			dir, err := tackDir()
			if err != nil {
				return nil, err
			}
			path = filepath.Join(dir, "playbook.yaml")
		}
		return &hook.TackHook{ConfigPath: path}, nil
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
		sshKey = sshkey.ExpandHome(strings.TrimSuffix(srv.SSHPubkey, ".pub"))
	}
	return user, sshKey
}

// applyHookOptions sets the post-create hook fields shared by launch and
// clone: the hook itself, --strict-hooks, SSH host-key policy, and the
// SSH user/key hooks connect with.
func applyHookOptions(opts *launch.Options, hk hook.Hook, f *launchFlags, srv *config.Server, sshInsecure bool) {
	opts.Hook = hk
	opts.StrictHooks = f.strictHooks
	opts.SSHInsecure = sshInsecure
	opts.User, opts.SSHKeyPath = hookSSHDefaults(srv)
}

func runLaunch(cmd *cobra.Command, name string, f *launchFlags) error {
	ctx := cmd.Context()
	f.bridgeSet = cmd.Flags().Changed("bridge")

	// Resolve hook flags before any config load / server resolution /
	// PVE call so --post-create + --tack (etc.) fail immediately with
	// zero network traffic.
	hk, err := resolveHook(f)
	if err != nil {
		return err
	}

	// buildClient loads config, resolves the server, emits the D-T4
	// verbose log line, and runs the TLS pin check — all before any PVE
	// API call.
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}

	if err := resolved.RequireNodeSSH("launch"); err != nil {
		return err
	}

	opts, err := resolveLaunchOptions(ctx, client, name, f, resolved, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	applyHookOptions(&opts, hk, f, resolved.Server, SSHInsecure())
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
func resolveLaunchOptions(ctx context.Context, client *pveclient.Client, name string, f *launchFlags, resolved *server.Resolved, stderr io.Writer) (launch.Options, error) {
	srv := resolved.Server

	node := firstNonEmpty(f.node, srv.Node)
	if node == "" {
		return launch.Options{}, fmt.Errorf("%w: no node configured; pass --node or run 'pmox init'", exitcode.ErrNotFound)
	}

	templateStr := firstNonEmpty(f.template, srv.Template)
	if templateStr == "" {
		return launch.Options{}, fmt.Errorf("%w: no template configured; pass --template or run 'pmox init'", exitcode.ErrNotFound)
	}
	templateID, _, err := resolveTemplate(ctx, client, node, templateStr)
	if err != nil {
		return launch.Options{}, err
	}

	opts, err := resolveVMSpec(f, resolved, stderr)
	if err != nil {
		return launch.Options{}, err
	}
	opts.Client = client
	opts.Node = node
	opts.Name = name
	opts.TemplateID = templateID
	return opts, nil
}

// resolveVMSpec resolves the per-VM resources shared by launch and
// clone — CPU, memory, disk, storage, snippet storage, bridge, wait
// budget and cloud-init path — layering flag > configured default >
// built-in. The bridge is the exception: it comes only from an explicit
// --bridge (see explicitBridge). The returned Options has
// Client/Node/Name/TemplateID unset.
// An empty storage is rejected: it would otherwise reach PVE as
// ide2=":cloudinit".
func resolveVMSpec(f *launchFlags, resolved *server.Resolved, stderr io.Writer) (launch.Options, error) {
	srv := resolved.Server

	storage := firstNonEmpty(f.storage, srv.Storage)
	if storage == "" {
		return launch.Options{}, fmt.Errorf("%w: no storage configured; pass --storage or run 'pmox init' (required for the cloud-init drive)", exitcode.ErrNotFound)
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
	memGB := f.memGB
	if memGB == 0 {
		memGB = defaultMemGB
	}
	diskGB := f.diskGB
	if diskGB == 0 {
		diskGB = defaultDiskGB
	}
	wait := f.wait
	if wait == 0 {
		wait = defaultWait
	}

	return launch.Options{
		CPU:            cpu,
		MemMB:          memGB * 1024,
		DiskSize:       fmt.Sprintf("%dG", diskGB),
		Storage:        storage,
		SnippetStorage: snippetStorage,
		Bridge:         explicitBridge(f),
		Wait:           wait,
		NoWaitSSH:      f.noWaitSSH,
		CloudInitPath:  cloudInitPath,
		Stderr:         stderr,
	}, nil
}

// explicitBridge returns the bridge to force onto the new VM's net0: the
// --bridge value when the flag was passed, else "" (keep the NIC cloned
// from the template/source VM). The configured server bridge is never
// applied here — it would silently move clones off their source bridge
// and needs VM.Config.Network on every launch.
func explicitBridge(f *launchFlags) string {
	if !f.bridgeSet {
		return ""
	}
	return f.bridge
}

// resolveSnippetStorage layers --snippet-storage > server.SnippetStorage
// > VM disk storage. When the final fallback to disk storage kicks in
// (no flag, no config), it emits a one-shot stderr warning pointing the
// user at `pmox init` for a permanent fix.
func resolveSnippetStorage(flag, configured, diskStorage string, stderr io.Writer) string {
	if flag != "" {
		return flag
	}
	if configured != "" {
		return configured
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "warning: no snippet_storage configured; falling back to %q. run 'pmox init' to set it permanently\n", diskStorage)
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
	data, err := os.ReadFile(sshkey.ExpandHome(path))
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
