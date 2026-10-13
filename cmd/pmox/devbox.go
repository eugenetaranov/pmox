package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/devbox/profile"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/sshkey"
)

// devboxPushTimeout bounds one copy of devbox-setup's files (~200 KB).
const devboxPushTimeout = 2 * time.Minute

func newDevboxCmd() *cobra.Command {
	return nounGroup("devbox", "Install devbox-setup on VMs", `devbox-setup is the on-VM installer for dev tools, languages, Docker,
AI agents and MCPJungle ('sudo devbox-setup' on the VM). pmox installs it
on every VM it launches; 'pmox devbox push' installs or refreshes it on
an existing VM, 'pmox devbox apply' applies profiles across VMs, and
'pmox devbox verify' checks them. Your team's own items and MCP servers
in ~/.config/pmox/devbox/catalog.d/*.toml and mcp-catalog/*.spec go with
every push.`,
		newDevboxPushCmd(),
		newDevboxApplyCmd(),
		newDevboxVerifyCmd(),
	)
}

func newDevboxPushCmd() *cobra.Command {
	f := &sshFlags{}
	cmd := &cobra.Command{
		Use:   "push [name|vmid]",
		Short: "Install or refresh devbox-setup on a VM",
		Long: `Copy this pmox version's devbox-setup to a VM over SSH. Saved choices
(/var/lib/devbox-setup) are kept, so 'sudo devbox-setup --yes' re-applies
them with the new version.

The VM argument is optional: when omitted, pmox picks the only pmox VM
or shows a picker. Needs passwordless sudo for the login user, as on
pmox's default cloud-init.`,
		Example: `  pmox devbox push web1
  pmox devbox push            # pick a VM`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runDevboxPush(cmd, args, f) },
	}
	addSSHFlags(cmd, f)
	return cmd
}

func runDevboxPush(cmd *cobra.Command, args []string, f *sshFlags) error {
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	arg, err := resolveTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	target, err := resolveSSHTarget(ctx, cmd, client, arg, f, resolved.URL, resolved.Server)
	if err != nil {
		return err
	}
	sp := startSpin(fmt.Sprintf("Installing devbox-setup on %s…", target.Name))
	err = devboxPushFn(ctx, target)
	sp.Stop()
	if err != nil {
		return explainSSHFailure(ctx, target, resolved.URL, fmt.Errorf("install devbox-setup on %s: %w", target.Name, err), true)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%sdevbox-setup installed on %s\n", doneMark(true), target.Name)
	fmt.Fprintf(cmd.ErrOrStderr(), "next: pmox shell %s, then 'sudo devbox-setup' (or 'sudo devbox-setup --yes' to re-apply saved choices)\n", target.Name)
	return nil
}

// applyForgetHostKey drops, at launch, a guest host key pinned for the new
// VM's IP: a VM deleted earlier may have had it (DHCP reuses addresses),
// and a brand-new VM's key can't be known yet. Without this the first SSH
// fails host key verification.
func applyForgetHostKey(opts *launch.Options) {
	opts.NewIPFn = func(ip string) {
		path, err := guestKnownHostsPath()
		if err != nil {
			return
		}
		_, _ = pvessh.KnownHostsPrune(path, func(host string) bool { return host != ip })
	}
}

// applyDevboxPush wires launch's devbox-setup phase to devboxPushFn, with
// the user and key hooks connect with.
func applyDevboxPush(opts *launch.Options) {
	if !opts.DevboxSetup {
		return
	}
	name, user, key := opts.Name, opts.User, opts.SSHKeyPath
	opts.PushDevboxFn = func(ctx context.Context, ip string) error {
		return devboxPushFn(ctx, &sshTarget{IP: ip, User: user, Key: key, Name: name})
	}
}

// devboxProfileRemote is where a launch's profile lands on the VM and the
// command that applies it (stdin: the profile).
const (
	devboxProfileRemote  = "/var/lib/devbox-setup/launch-profile.toml"
	devboxProfileCommand = "sudo -n sh -c 'umask 077 && mkdir -p /var/lib/devbox-setup && cat > " + devboxProfileRemote +
		" && devbox-setup --profile " + devboxProfileRemote + " --yes'"
	devboxProfileTimeout = 45 * time.Minute
)

// applyDevboxProfileOpt wires --devbox-profile (or the server's
// devbox_profile) into launch: the file is checked now, before anything
// is created, and applied over SSH once devbox-setup is installed.
func applyDevboxProfileOpt(opts *launch.Options, flagPath string, srv *config.Server) error {
	path := flagPath
	if path == "" && srv != nil {
		path = srv.DevboxProfile
	}
	if path == "" {
		return nil
	}
	if !opts.DevboxSetup {
		return fmt.Errorf("--devbox-profile needs devbox-setup, which is off for this server (devbox_setup: false)")
	}
	if !bootstrap.HasGoBuilds() {
		return fmt.Errorf("--devbox-profile needs the Go devbox-setup, which this pmox build doesn't include (build with 'task build')")
	}
	b, err := os.ReadFile(sshkey.ExpandHome(path))
	if err != nil {
		return fmt.Errorf("devbox profile: %w", err)
	}
	if _, err := profile.Parse(path, b); err != nil {
		return fmt.Errorf("devbox profile: %w", err)
	}
	name, user, key := opts.Name, opts.User, opts.SSHKeyPath
	opts.DevboxProfileFn = func(ctx context.Context, ip string, stdout, stderr io.Writer) error {
		return devboxProfileFn(ctx, &sshTarget{IP: ip, User: user, Key: key, Name: name}, b, stdout, stderr)
	}
	return nil
}

// devboxProfileFn sends the profile to the target and applies it,
// streaming the output. A seam for tests.
var devboxProfileFn = func(ctx context.Context, target *sshTarget, prof []byte, stdout, stderr io.Writer) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, devboxProfileTimeout)
	defer cancel()
	args := devboxPushArgs(target, guestHostKeyOpts())
	args[len(args)-1] = devboxProfileCommand
	c := exec.CommandContext(ctx, sshPath, args...)
	c.Stdin = bytes.NewReader(prof)
	c.Stdout, c.Stderr = stdout, stderr
	return c.Run()
}

// devboxPushFn streams bootstrap.Tarball to the target and installs it
// with bootstrap.InstallCommand. A seam for tests.
var devboxPushFn = func(ctx context.Context, target *sshTarget) error {
	team, err := devboxTeamFiles()
	if err != nil {
		return err
	}
	tgz, err := bootstrap.Tarball(team...)
	if err != nil {
		return err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, devboxPushTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, sshPath, devboxPushArgs(target, guestHostKeyOpts())...)
	c.Stdin = bytes.NewReader(tgz)
	out, err := c.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, lastLine(msg))
		}
		return err
	}
	return nil
}

// Commands pmox delete runs on a VM: is there anything for
// `devbox-setup forget` to remove, and run it.
const (
	devboxCreatedCheck  = `if sudo -n grep -qs '"kind"' /var/lib/devbox-setup/created.json; then echo present; fi`
	devboxForgetCommand = "sudo -n devbox-setup forget --yes"
)

// devboxRemoteFn runs command on the target over ssh (no prompts) and
// returns its combined output. A seam for tests.
var devboxRemoteFn = func(ctx context.Context, target *sshTarget, command string, timeout time.Duration) (string, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return "", errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := devboxPushArgs(target, guestHostKeyOpts())
	args[len(args)-1] = command
	out, err := exec.CommandContext(ctx, sshPath, args...).CombinedOutput()
	return string(out), err
}

func devboxPushArgs(target *sshTarget, hostKeyOpts []string) []string {
	args := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}, hostKeyOpts...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	return append(args, fmt.Sprintf("%s@%s", target.User, target.IP), bootstrap.InstallCommand)
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
