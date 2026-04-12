package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

type remoteArg struct {
	vmRef      string
	remotePath string
}

func parseRemoteArg(arg string) (ref string, remotePath string, isRemote bool) {
	i := strings.Index(arg, ":")
	if i < 0 {
		return "", "", false
	}
	return arg[:i], arg[i+1:], true
}

func resolveTransferArgs(args []string) (local string, remote remoteArg, localIsSource bool, err error) {
	srcRef, srcPath, srcRemote := parseRemoteArg(args[0])
	dstRef, dstPath, dstRemote := parseRemoteArg(args[1])

	switch {
	case srcRemote && dstRemote:
		return "", remoteArg{}, false, fmt.Errorf("VM-to-VM transfer is not supported; exactly one argument must reference a VM")
	case !srcRemote && !dstRemote:
		return "", remoteArg{}, false, fmt.Errorf("exactly one argument must reference a VM using <name>:<path> syntax")
	case srcRemote:
		return args[1], remoteArg{vmRef: srcRef, remotePath: srcPath}, false, nil
	default:
		return args[0], remoteArg{vmRef: dstRef, remotePath: dstPath}, true, nil
	}
}

func sshOptionArgs(target *sshTarget) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	return args
}

func sshOptionString(target *sshTarget) string {
	parts := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if target.Key != "" {
		parts = append(parts, "-i", target.Key)
	}
	return strings.Join(parts, " ")
}

func extraArgsAfterDash() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

// scpRunFn runs scp. Tests override this.
var scpRunFn = func(bin string, args []string) error {
	c := exec.Command(bin, args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// rsyncRunFn runs rsync. Tests override this.
var rsyncRunFn = func(bin string, args []string) error {
	c := exec.Command(bin, args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func newCpCmd() *cobra.Command {
	f := &sshFlags{}
	var recursive bool
	cmd := &cobra.Command{
		Use:   "cp <source> <destination>",
		Short: "Copy files between local host and a VM via scp",
		Long: `Copy files between the local host and a pmox-managed VM using scp.
Exactly one of source or destination must use <name>:<path> syntax
to identify the remote side.

Examples:
  pmox cp ./app.tar.gz web1:/tmp/
  pmox cp web1:/var/log/syslog ./logs/
  pmox cp -r ./config/ web1:/etc/app/
  pmox cp ./big.tar web1:/tmp/ -- -l 1000`,
		Args:               cobra.ExactArgs(2),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCp(cmd, args, f, recursive)
		},
	}
	addSSHFlags(cmd, f)
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "copy directories recursively")
	return cmd
}

func runCp(cmd *cobra.Command, args []string, f *sshFlags, recursive bool) error {
	scpPath, err := exec.LookPath("scp")
	if err != nil {
		return fmt.Errorf("scp binary not found on PATH; install OpenSSH to use pmox cp")
	}

	localArg, remote, localIsSource, err := resolveTransferArgs(args)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client, srv, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, remote.vmRef, f, srv.SSHPubkey)
	if err != nil {
		return err
	}

	scpArgs := buildScpArgs(scpPath, target, localArg, remote.remotePath, localIsSource, recursive, extraArgsAfterDash())
	return scpRunFn(scpPath, scpArgs)
}

func buildScpArgs(scpPath string, target *sshTarget, localPath, remotePath string, localIsSource, recursive bool, extra []string) []string {
	args := []string{scpPath}
	args = append(args, sshOptionArgs(target)...)
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, extra...)

	remoteSpec := fmt.Sprintf("%s@%s:%s", target.User, target.IP, remotePath)
	if localIsSource {
		args = append(args, localPath, remoteSpec)
	} else {
		args = append(args, remoteSpec, localPath)
	}
	return args
}

func newSyncCmd() *cobra.Command {
	f := &sshFlags{}
	cmd := &cobra.Command{
		Use:   "sync <source> <destination>",
		Short: "Sync files between local host and a VM via rsync",
		Long: `Synchronize files between the local host and a pmox-managed VM using
rsync over SSH. Exactly one of source or destination must use
<name>:<path> syntax to identify the remote side.

Examples:
  pmox sync ./src/ web1:/opt/app/
  pmox sync web1:/var/log/ ./logs/
  pmox sync ./src/ web1:/opt/app/ -- --delete --exclude .git`,
		Args:               cobra.ExactArgs(2),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, args, f)
		},
	}
	addSSHFlags(cmd, f)
	return cmd
}

func runSync(cmd *cobra.Command, args []string, f *sshFlags) error {
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return fmt.Errorf("rsync binary not found on PATH; install rsync to use pmox sync")
	}

	localArg, remote, localIsSource, err := resolveTransferArgs(args)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client, srv, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, remote.vmRef, f, srv.SSHPubkey)
	if err != nil {
		return err
	}

	rsyncArgs := buildRsyncArgs(rsyncPath, target, localArg, remote.remotePath, localIsSource, extraArgsAfterDash())
	return rsyncRunFn(rsyncPath, rsyncArgs)
}

func buildRsyncArgs(rsyncPath string, target *sshTarget, localPath, remotePath string, localIsSource bool, extra []string) []string {
	args := []string{rsyncPath}
	args = append(args, "-e", sshOptionString(target))
	args = append(args, extra...)

	remoteSpec := fmt.Sprintf("%s@%s:%s", target.User, target.IP, remotePath)
	if localIsSource {
		args = append(args, localPath, remoteSpec)
	} else {
		args = append(args, remoteSpec, localPath)
	}
	return args
}
