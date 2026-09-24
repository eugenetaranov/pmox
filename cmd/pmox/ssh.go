package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const (
	sshStartTimeout = 120 * time.Second
	sshIPTimeout    = 60 * time.Second
	sshReadyTimeout = 30 * time.Second
)

type sshFlags struct {
	user     string
	identity string
	force    bool
}

func addSSHFlags(cmd *cobra.Command, f *sshFlags) {
	cmd.Flags().StringVarP(&f.user, "user", "u", "", "SSH login user (defaults to server config 'user', then 'pmox')")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH private key")
	cmd.Flags().BoolVar(&f.force, "force", false, "bypass the pmox tag check")
}

func newShellCmd() *cobra.Command {
	f := &sshFlags{}
	cmd := &cobra.Command{
		Use:   "shell [name|vmid]",
		Short: "Open an interactive SSH session to a VM",
		Long: `Open an interactive SSH shell on a pmox-managed VM. The argument
may be a VM name (e.g. "web1") or numeric VMID (e.g. "104"). If
omitted, pmox auto-selects the only pmox VM when one exists, or
shows an interactive picker when there are several.

If the VM is stopped, shell auto-starts it and waits for SSH
readiness before connecting.

The default login user is "pmox" (the cloud-init user). The default
identity key is derived from the configured SSH public key by
stripping the .pub suffix. Override with --user / --identity.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, args, f)
		},
	}
	addSSHFlags(cmd, f)
	return cmd
}

func newExecCmd() *cobra.Command {
	f := &sshFlags{}
	cmd := &cobra.Command{
		Use:   "exec [name|vmid] -- <command> [args...]",
		Short: "Run a command on a VM over SSH",
		Long: `Run a single command on a pmox-managed VM over SSH and return its
output and exit code. The -- separator is required between the VM
argument and the remote command.

The VM argument is optional: when omitted, pmox auto-selects the
only pmox VM when one exists, or shows an interactive picker when
there are several.

If the VM is stopped, exec auto-starts it and waits for SSH
readiness before running the command.

The default login user is "pmox". Override with --user / --identity.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args, f)
		},
	}
	addSSHFlags(cmd, f)
	return cmd
}

// sshExecFn is the function used to replace the process for shell.
// Tests override this to capture args without actually exec'ing.
var sshExecFn = syscall.Exec

// sshRunFn is the function used to run a command for exec.
// Tests override this to capture args.
var sshRunFn = func(sshPath string, args []string) error {
	c := exec.Command(sshPath, args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runShell(cmd *cobra.Command, args []string, f *sshFlags) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh binary not found on PATH; install OpenSSH to use pmox shell")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	client, srv, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return err
	}

	arg, err := resolveTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, arg, f, srv.User, srv.SSHPubkey)
	if err != nil {
		return err
	}

	sshArgs := buildSSHArgs(sshPath, target, guestHostKeyOpts(), nil)
	return sshExecFn(sshPath, sshArgs, os.Environ())
}

func runExec(cmd *cobra.Command, args []string, f *sshFlags) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh binary not found on PATH; install OpenSSH to use pmox exec")
	}

	// Extract remote command via the literal "--" separator in os.Args.
	var remoteArgs []string
	for i, a := range os.Args {
		if a == "--" {
			remoteArgs = os.Args[i+1:]
			break
		}
	}
	if len(remoteArgs) == 0 {
		return fmt.Errorf("no command specified; use: pmox exec [name|vmid] -- <command> [args...]")
	}

	// Determine whether a VM positional was supplied before "--".
	// cobra's ArgsLenAtDash() returns the number of positional args
	// before "--". 0 = no VM arg, 1 = VM arg present.
	var vmArgs []string
	if n := cmd.ArgsLenAtDash(); n == 1 {
		vmArgs = []string{args[0]}
	} else if n > 1 {
		return fmt.Errorf("exec takes at most one VM positional argument before `--`")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	client, srv, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return err
	}

	vmArg, err := resolveTargetArg(ctx, client, vmArgs, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, vmArg, f, srv.User, srv.SSHPubkey)
	if err != nil {
		return err
	}

	sshArgs := buildSSHArgs(sshPath, target, guestHostKeyOpts(), remoteArgs)
	return sshRunFn(sshPath, sshArgs)
}

type sshTarget struct {
	IP   string
	User string
	Key  string
}

func resolveSSHTarget(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string, f *sshFlags, configUser, configPubkey string) (*sshTarget, error) {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return nil, err
	}

	if !f.force && !vm.HasPMOXTag(ref.Tags) {
		return nil, fmt.Errorf("refusing to connect to VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)
	}

	ip, err := getOrStartVM(ctx, cmd, client, ref)
	if err != nil {
		return nil, err
	}

	key, err := resolveIdentityKey(f.identity, configPubkey)
	if err != nil {
		return nil, err
	}

	return &sshTarget{
		IP:   ip,
		User: firstNonEmpty(f.user, configUser, defaultUser),
		Key:  key,
	}, nil
}

func getOrStartVM(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, ref *vm.Ref) (string, error) {
	status, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			return "", fmt.Errorf("VM %q (vmid %d) not found", ref.Name, ref.VMID)
		}
		return "", fmt.Errorf("get status for vm %d: %w", ref.VMID, err)
	}

	if status.Status == "stopped" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Starting VM %q...\n", ref.Name)
		upid, err := client.Start(ctx, ref.Node, ref.VMID)
		if err != nil {
			return "", fmt.Errorf("start vm %d: %w", ref.VMID, err)
		}
		if err := client.WaitTask(ctx, ref.Node, upid, sshStartTimeout); err != nil {
			return "", fmt.Errorf("start vm %d: %w", ref.VMID, err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Waiting for IP...\n")
		ip, err := launch.WaitForIP(ctx, client, ref.Node, ref.VMID, sshIPTimeout)
		if err != nil {
			return "", err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Waiting for SSH...\n")
		if err := launch.WaitForSSH(ctx, ip, sshReadyTimeout); err != nil {
			return "", err
		}
		return ip, nil
	}

	ifaces, err := client.AgentNetwork(ctx, ref.Node, ref.VMID)
	if err != nil {
		return "", fmt.Errorf("VM %q is running but guest agent is not responding; is qemu-guest-agent installed?", ref.Name)
	}
	ip := launch.PickIPv4(ifaces)
	if ip == "" {
		return "", fmt.Errorf("VM %q is running but guest agent returned no usable IPv4 address", ref.Name)
	}
	return ip, nil
}

func resolveIdentityKey(flagValue, configPubkey string) (string, error) {
	if flagValue != "" {
		expanded := expandHome(flagValue)
		if _, err := os.Stat(expanded); err != nil {
			return "", fmt.Errorf("identity key %q not found", flagValue)
		}
		return expanded, nil
	}

	if configPubkey == "" {
		return "", nil
	}

	privPath := derivePrivateKeyPath(configPubkey)
	expanded := expandHome(privPath)
	if _, err := os.Stat(expanded); err != nil {
		return "", fmt.Errorf("derived private key %q (from %s) not found; pass --identity explicitly", privPath, configPubkey)
	}
	return expanded, nil
}

func derivePrivateKeyPath(pubkeyPath string) string {
	return strings.TrimSuffix(pubkeyPath, ".pub")
}

func buildSSHArgs(sshPath string, target *sshTarget, hostKeyOpts, extraArgs []string) []string {
	args := []string{sshPath}
	args = append(args, hostKeyOpts...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.User, target.IP))
	args = append(args, extraArgs...)
	return args
}

// guestKnownHostsPath returns the pmox-managed known_hosts file used for
// guest-VM SSH (shell/exec/cp/sync/mount). It is kept separate from the
// PVE node known_hosts so guest and node host keys don't intermingle.
func guestKnownHostsPath() (string, error) {
	base, err := pvessh.KnownHostsPath()
	if err != nil {
		return "", err
	}
	return base + "_guests", nil
}

// guestHostKeyOpts returns the ssh/scp "-o" options controlling host-key
// verification for guest-VM connections. By default it enables TOFU:
// an unknown host key is pinned into the pmox-managed known_hosts on
// first connect, and a later key change aborts the connection
// (StrictHostKeyChecking=accept-new) — protecting against MITM after the
// first successful connect. --ssh-insecure (with its one-shot warning)
// restores the legacy behavior that skips verification entirely.
func guestHostKeyOpts() []string {
	if SSHInsecure() {
		return []string{
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		}
	}
	path, err := guestKnownHostsPath()
	if err != nil || strings.ContainsAny(path, " \t") {
		// Either the config dir is unresolvable, or the path contains
		// whitespace. rsync's -e value (used by sync/mount) is
		// whitespace-split with no quoting, so a spaced UserKnownHostsFile
		// would corrupt the ssh invocation. Fall back to accept-new
		// against ssh's default known_hosts — still TOFU, just not the
		// pmox-managed file — rather than skipping verification.
		return []string{"-o", "StrictHostKeyChecking=accept-new"}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	return []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + path,
	}
}
