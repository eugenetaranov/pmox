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

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/sshkey"
)

// yardoPushTimeout bounds installing yardo (a download of a few MB on
// the VM) and syncing the team's files.
const yardoPushTimeout = 5 * time.Minute

func newYardoCmd() *cobra.Command {
	return nounGroup("yardo", "Install and drive yardo on VMs", `yardo (github.com/eugenetaranov/yardo) sets up a dev machine for you and
your AI agents: dev tools, languages, Docker, coding agents and MCP
servers ('sudo yardo' on the VM). pmox installs its release on every VM
it launches; 'pmox yardo push' installs or refreshes it on an existing
VM, 'pmox yardo apply' applies profiles across VMs, 'pmox yardo verify'
checks them, and 'pmox yardo gitkey' sends a git SSH key. Your team's own
items and MCP servers in ~/.config/pmox/yardo/catalog.d/*.toml and
mcp-catalog/*.spec go with every push. pmox installs yardo's latest
release; PMOX_YARDO_VERSION=<tag> installs another.`,
		newYardoPushCmd(),
		newYardoApplyCmd(),
		newYardoVerifyCmd(),
		newYardoGitKeyCmd(),
	)
}

func newYardoPushCmd() *cobra.Command {
	f := &sshFlags{}
	cmd := &cobra.Command{
		Use:   "push [name|vmid]",
		Short: "Install or refresh yardo on a VM",
		Long: `Install yardo's release on a VM (the VM downloads it from GitHub and
checks its checksum), and sync your team's yardo items and MCP specs.
Saved choices (/var/lib/yardo) are kept, so 'sudo yardo --yes'
re-applies them with the new version.

The VM argument is optional: when omitted, pmox picks the only pmox VM
or shows a picker. Needs passwordless sudo for the login user, as on
pmox's default cloud-init.`,
		Example: `  pmox yardo push web1
  pmox yardo push            # pick a VM`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runYardoPush(cmd, args, f) },
	}
	addSSHFlags(cmd, f)
	return cmd
}

func runYardoPush(cmd *cobra.Command, args []string, f *sshFlags) error {
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
	sp := startSpin(fmt.Sprintf("Installing yardo on %s…", target.Name))
	err = yardoPushFn(ctx, target)
	sp.Stop()
	if err != nil {
		return explainSSHFailure(ctx, target, resolved.URL, fmt.Errorf("install yardo on %s: %w", target.Name, err), true)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%syardo installed on %s\n", doneMark(true), target.Name)
	fmt.Fprintf(cmd.ErrOrStderr(), "next: pmox shell %s, then 'sudo yardo' (or 'sudo yardo --yes' to re-apply saved choices)\n", target.Name)
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

// applyYardoPush wires launch's yardo phase to yardoPushFn, with
// the user and key hooks connect with.
func applyYardoPush(opts *launch.Options) {
	if !opts.Yardo {
		return
	}
	name, user, key := opts.Name, opts.User, opts.SSHKeyPath
	opts.PushYardoFn = func(ctx context.Context, ip string) error {
		return yardoPushFn(ctx, &sshTarget{IP: ip, User: user, Key: key, Name: name})
	}
}

// yardoProfileRemote is where a launch's profile lands on the VM and the
// command that applies it (stdin: the profile).
const (
	yardoProfileRemote  = "/var/lib/yardo/launch-profile.toml"
	yardoProfileCommand = "sudo -n sh -c 'umask 077 && mkdir -p /var/lib/yardo && cat > " + yardoProfileRemote +
		" && yardo --profile " + yardoProfileRemote + " --yes'"
	yardoProfileTimeout = 45 * time.Minute
)

// applyYardoProfileOpt wires --yardo-profile (or the server's
// yardo_profile) into launch: the file is checked now, before anything
// is created, and applied over SSH once yardo is installed.
func applyYardoProfileOpt(opts *launch.Options, flagPath string, srv *config.Server) error {
	path := flagPath
	if path == "" && srv != nil {
		path = srv.YardoProfile
	}
	if path == "" {
		return nil
	}
	if !opts.Yardo {
		return fmt.Errorf("--yardo-profile needs yardo, which is off for this server (yardo: false)")
	}
	b, err := os.ReadFile(sshkey.ExpandHome(path))
	if err != nil {
		return fmt.Errorf("yardo profile: %w", err)
	}
	if err := checkTOML(path, b); err != nil {
		return fmt.Errorf("yardo profile: %w", err)
	}
	name, user, key := opts.Name, opts.User, opts.SSHKeyPath
	opts.YardoProfileFn = func(ctx context.Context, ip string, stdout, stderr io.Writer) error {
		return yardoProfileFn(ctx, &sshTarget{IP: ip, User: user, Key: key, Name: name}, b, stdout, stderr)
	}
	return nil
}

// yardoProfileFn sends the profile to the target and applies it,
// streaming the output. A seam for tests.
var yardoProfileFn = func(ctx context.Context, target *sshTarget, prof []byte, stdout, stderr io.Writer) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, yardoProfileTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, sshPath, yardoSSHArgs(target, guestHostKeyOpts(), yardoProfileCommand)...)
	c.Stdin = bytes.NewReader(prof)
	c.Stdout, c.Stderr = stdout, stderr
	return c.Run()
}

// yardoPushFn installs yardo's release on the target (it downloads and
// checks it) and syncs the team's files. A seam for tests.
var yardoPushFn = func(ctx context.Context, target *sshTarget) error {
	team, err := yardoTeamFiles()
	if err != nil {
		return err
	}
	tgz, err := bootstrap.TeamTarball(team)
	if err != nil {
		return err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, yardoPushTimeout)
	defer cancel()
	for _, step := range []struct {
		command string
		stdin   []byte
	}{{bootstrap.YardoInstallCommand(bootstrap.Yardo()), nil}, {bootstrap.TeamSyncCommand, tgz}} {
		c := exec.CommandContext(ctx, sshPath, yardoSSHArgs(target, guestHostKeyOpts(), step.command)...)
		if step.stdin != nil {
			c.Stdin = bytes.NewReader(step.stdin)
		}
		if out, err := c.CombinedOutput(); err != nil {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				return fmt.Errorf("%w: %s", err, lastLine(msg))
			}
			return err
		}
	}
	return nil
}

// checkTOML checks that b is TOML. yardo on the VM checks the rest
// (keys, items, secrets) and says what's wrong.
func checkTOML(name string, b []byte) error {
	var v map[string]any
	if err := toml.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Commands pmox delete runs on a VM: is there anything for
// `yardo forget` to remove, and run it.
const (
	yardoCreatedCheck  = `if sudo -n grep -qs '"kind"' /var/lib/yardo/created.json; then echo present; fi`
	yardoForgetCommand = "sudo -n yardo forget --yes"
)

// yardoRemoteFn runs command on the target over ssh (no prompts) and
// returns its combined output. A seam for tests.
var yardoRemoteFn = func(ctx context.Context, target *sshTarget, command string, timeout time.Duration) (string, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return "", errors.New("ssh binary not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, sshPath, yardoSSHArgs(target, guestHostKeyOpts(), command)...).CombinedOutput()
	return string(out), err
}

// yardoSSHArgs are ssh's arguments to run command on the target, no prompts.
func yardoSSHArgs(target *sshTarget, hostKeyOpts []string, command string) []string {
	args := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}, hostKeyOpts...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	return append(args, fmt.Sprintf("%s@%s", target.User, target.IP), command)
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
