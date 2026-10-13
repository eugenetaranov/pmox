package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/sshkey"
)

// gitHost is a host name with an optional :port (yardo checks it again).
var gitHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)

func newYardoGitKeyCmd() *cobra.Command {
	f := &sshFlags{}
	var hosts []string
	cmd := &cobra.Command{
		Use:   "gitkey [name|vmid] <private key file>",
		Short: "Send an SSH key for git (a deploy key, or yours) to a VM",
		Long: `Copy an SSH private key to a VM for git, and set it up there with
yardo's "SSH key for git" item: the key goes to
~/.ssh/yardo_git_key (0600), ~/.ssh/config makes every connection to
github.com (or the --host ones) use it, and GitHub's host keys are added.
It then works for any repo the key may reach: the one repo of a deploy
key, or all of yours for a key on your GitHub account.

The key goes over SSH to 'sudo yardo gitkey' on stdin; pmox keeps no copy, and
yardo saves only which hosts it is for. Re-run to replace it.`,
		Example: `  pmox yardo gitkey text2cad ~/.ssh/text2cad_deploy
  pmox yardo gitkey web1 ~/.ssh/id_ed25519 --host github.com --host gitlab.example.com`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runYardoGitKey(cmd, args, f, hosts)
		},
	}
	addSSHFlags(cmd, f)
	cmd.Flags().StringArrayVar(&hosts, "host", nil, "a git host to use the key for (repeatable; default github.com)")
	return cmd
}

func runYardoGitKey(cmd *cobra.Command, args []string, f *sshFlags, hosts []string) error {
	keyFile := args[len(args)-1]
	key, err := os.ReadFile(sshkey.ExpandHome(keyFile))
	if err != nil {
		return fmt.Errorf("key: %w", err)
	}
	if !bytes.Contains(key, []byte("PRIVATE KEY-----")) {
		if bytes.HasPrefix(key, []byte("ssh-")) || bytes.HasPrefix(key, []byte("ecdsa-")) {
			return fmt.Errorf("%s is a public key; give the private one (without .pub)", keyFile)
		}
		return fmt.Errorf("%s isn't an SSH private key", keyFile)
	}
	command := "sudo -n yardo gitkey"
	for _, h := range hosts {
		if !gitHost.MatchString(h) {
			return fmt.Errorf("--host %q is not a host name", h)
		}
		command += " --host " + h
	}
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	arg, err := resolveTargetArg(ctx, client, args[:len(args)-1], cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	target, err := resolveSSHTarget(ctx, cmd, client, arg, f, resolved.URL, resolved.Server)
	if err != nil {
		return err
	}
	// The VM's yardo must know the gitkey item: push this version.
	sp := startSpin(fmt.Sprintf("Updating yardo on %s…", target.Name))
	err = yardoPushFn(ctx, target)
	sp.Stop()
	if err != nil {
		return explainSSHFailure(ctx, target, resolved.URL, fmt.Errorf("install yardo on %s: %w", target.Name, err), true)
	}
	sp = startSpin(fmt.Sprintf("Setting up the git key on %s…", target.Name))
	out, err := yardoFleetRunFn(ctx, target, command, key)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("yardo gitkey on %s: %w: %s", target.Name, err, lastLines(out, 4))
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, "! ") || strings.Contains(l, "git SSH key") {
			fmt.Fprintln(cmd.ErrOrStderr(), l)
		}
	}
	h := "github.com"
	if len(hosts) > 0 {
		h = strings.Join(hosts, ", ")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%sgit on %s uses %s for %s\n", doneMark(true), target.Name, keyFile, h)
	fmt.Fprintf(cmd.ErrOrStderr(), "try: pmox exec %s -- ssh -T git@%s\n", target.Name, strings.SplitN(strings.Split(h, ", ")[0], ":", 2)[0])
	return nil
}
