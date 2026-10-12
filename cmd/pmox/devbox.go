package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/launch"
)

// devboxPushTimeout bounds one copy of devbox-setup's files (~200 KB).
const devboxPushTimeout = 2 * time.Minute

func newDevboxCmd() *cobra.Command {
	return nounGroup("devbox", "Install devbox-setup on VMs", `devbox-setup is the on-VM installer for dev tools, languages, Docker,
AI agents and MCPJungle ('sudo devbox-setup' on the VM). pmox installs it
on every VM it launches; 'pmox devbox push' installs or refreshes it on
an existing VM.`,
		newDevboxPushCmd(),
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

// devboxPushFn streams bootstrap.Tarball to the target and installs it
// with bootstrap.InstallCommand. A seam for tests.
var devboxPushFn = func(ctx context.Context, target *sshTarget) error {
	tgz, err := bootstrap.Tarball()
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
