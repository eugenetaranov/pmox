package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/tui/target"
)

// ensureVMRef resolves the VM side of a cp/sync transfer. An explicit
// ref (name or vmid) is returned as-is; an empty ref — the bare ":path"
// form — triggers the shared VM picker, mirroring how `pmox mount`
// resolves a bare remote path.
func ensureVMRef(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	picked, err := vmPickFn(ctx, client)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(picked.VMID), nil
}

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

// resolveCpSyncArgs returns cp/sync's source and destination the way
// resolveTransferArgs does, either parsed straight from args or, with
// none given on a terminal, by asking for a direction and then the local
// path and <vm>:<path> fields in that direction's order
// (openspec/specs/remote-target-input). Non-interactively with zero
// args this is still the same hard error cp/sync always gave.
func resolveCpSyncArgs(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, f *sshFlags, serverURL string, srv *config.Server, args []string, usage, example string) (localArg string, remote remoteArg, localIsSource bool, err error) {
	args = positionalArgs(cmd, args)
	if len(args) == 2 {
		return resolveTransferArgs(args)
	}
	if !tui.Interactive() || outputMode == "json" {
		return "", remoteArg{}, false, fmt.Errorf("%w: expected 2 arguments, got 0 — usage: %s (example: %s)", exitcode.ErrUserInput, usage, example)
	}

	dir, err := selectDirectionFn("Direction", []huh.Option[string]{
		huh.NewOption("Upload: local → VM", "upload"),
		huh.NewOption("Download: VM → local", "download"),
	})
	if err != nil {
		return "", remoteArg{}, false, err
	}
	vms, err := targetVMsFn(ctx, client)
	if err != nil {
		return "", remoteArg{}, false, err
	}
	upload := dir == "upload"
	local := &target.Local{Title: "Local destination"}
	rem := &target.Remote{Title: "Remote source", VMs: vms, List: targetLister(f, serverURL, srv)}
	fields := []target.Field{rem, local}
	if upload {
		local.Title, rem.Title = "Local source", "Remote destination"
		// ~/<local name>: never the bare home directory, which a
		// directory sync would fill with the source's contents.
		rem.DefaultPath = func(prev []string) string {
			if p := mountDefaultPath(prev[0]); p != "" {
				return "~/" + strings.TrimPrefix(p, "/mnt/")
			}
			return ""
		}
		fields = []target.Field{local, rem}
	}
	if err := targetRunFn(ctx, fields...); err != nil {
		return "", remoteArg{}, false, err
	}
	v, p := rem.Result()
	return local.Result(), remoteArg{vmRef: v.Name, remotePath: p}, upload, nil
}

// selectDirectionFn asks upload vs download. A seam for tests.
var selectDirectionFn = tui.Select

// isLocalDir reports whether p is an existing local directory.
func isLocalDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ensureTransferDir confirms and creates a missing destination directory
// before cp/sync: remote for uploads, local for downloads. wholeDir is
// set when the destination itself must exist (sync of a directory).
func ensureTransferDir(ctx context.Context, cmd *cobra.Command, sshT *sshTarget, localArg, remotePath string, localIsSource, wholeDir, mkdir bool) error {
	if localIsSource {
		return ensureRemoteDir(ctx, cmd, sshT, dirToCheck(remotePath, wholeDir, remoteParent), mkdir)
	}
	return ensureLocalDir(cmd, dirToCheck(localArg, false, localParent), mkdir)
}

// scpOptionArgs returns the scp "-o" host-key options plus an optional
// identity flag. hostKeyOpts is computed once per command by
// guestHostKeyOpts so TOFU vs --ssh-insecure behavior is consistent.
func scpOptionArgs(target *sshTarget, hostKeyOpts []string) []string {
	args := append([]string{}, hostKeyOpts...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	return args
}

// rsyncSSHOption builds the value for rsync's -e flag: the ssh command
// plus the same host-key options used everywhere else.
func rsyncSSHOption(target *sshTarget, hostKeyOpts []string) string {
	parts := append([]string{"ssh"}, hostKeyOpts...)
	if target.Key != "" {
		parts = append(parts, "-i", target.Key)
	}
	return strings.Join(parts, " ")
}

// extraArgsAfterDash returns the pass-through arguments that followed a
// literal "--" on cmd's command line (nil if there was none).
func extraArgsAfterDash(cmd *cobra.Command) []string {
	n := cmd.ArgsLenAtDash()
	if n < 0 {
		return nil
	}
	return cmd.Flags().Args()[n:]
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
	var recursive, mkdir bool
	cmd := &cobra.Command{
		Use:   "cp <source> <destination>",
		Short: "Copy files to or from a VM",
		Long: `Copy files between the local host and a pmox-managed VM using scp.
Exactly one of source or destination must use <name>:<path> syntax
to identify the remote side.

Examples:
  pmox cp ./app.tar.gz web1:/tmp/
  pmox cp web1:/var/log/syslog ./logs/
  pmox cp -r ./config/ web1:/etc/app/
  pmox cp ./big.tar web1:/tmp/ -- -l 1000

On a terminal, 'pmox cp' alone asks for a direction, then the local
path and the <vm>:<path> target (Tab completes the VM and remote
directories; one pmox VM is filled in for you). A remote path not
starting with / or ~/ is relative to the login home.

A missing destination directory is confirmed and created; --mkdir
creates it without asking.`,
		Args:               zeroOrExactArgs(2, "pmox cp <source> <destination>", "pmox cp ./app.tar web1:/tmp/"),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCp(cmd, args, f, recursive, mkdir)
		},
	}
	addSSHFlags(cmd, f)
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "copy directories recursively")
	cmd.Flags().BoolVar(&mkdir, "mkdir", false, "create a missing destination directory without asking")
	return cmd
}

func runCp(cmd *cobra.Command, args []string, f *sshFlags, recursive, mkdir bool) error {
	scpPath, err := exec.LookPath("scp")
	if err != nil {
		return fmt.Errorf("scp binary not found on PATH; install OpenSSH to use pmox cp")
	}

	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server

	localArg, remote, localIsSource, err := resolveCpSyncArgs(ctx, cmd, client, f, resolved.URL, srv, args, "pmox cp <source> <destination>", "pmox cp ./app.tar web1:/tmp/")
	if err != nil {
		return err
	}

	remote.vmRef, err = ensureVMRef(ctx, cmd, client, remote.vmRef)
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, remote.vmRef, f, resolved.URL, srv)
	if err != nil {
		return err
	}

	if err := ensureTransferDir(ctx, cmd, target, localArg, remote.remotePath, localIsSource, false, mkdir); err != nil {
		return err
	}

	scpArgs := buildScpArgs(scpPath, target, localArg, remote.remotePath, localIsSource, recursive, guestHostKeyOpts(), extraArgsAfterDash(cmd))
	return explainSSHFailure(ctx, target, resolved.URL, scpRunFn(scpPath, scpArgs), false)
}

func buildScpArgs(scpPath string, target *sshTarget, localPath, remotePath string, localIsSource, recursive bool, hostKeyOpts, extra []string) []string {
	args := []string{scpPath}
	args = append(args, scpOptionArgs(target, hostKeyOpts)...)
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
	var noArchive, mkdir bool
	cmd := &cobra.Command{
		Use:   "sync <source> <destination>",
		Short: "Sync a directory to or from a VM",
		Long: `Synchronize files between the local host and a pmox-managed VM using
rsync over SSH. Exactly one of source or destination must use
<name>:<path> syntax to identify the remote side.

Examples:
  pmox sync ./src/ web1:/opt/app/
  pmox sync web1:/var/log/ ./logs/
  pmox sync ./src/ web1:/opt/app/ -- --delete --exclude .git

rsync runs with -a (recursive, keeps attributes) unless --no-archive.

On a terminal, 'pmox sync' alone asks for a direction, then the local
path and the <vm>:<path> target (Tab completes the VM and remote
directories; one pmox VM is filled in for you). A remote path not
starting with / or ~/ is relative to the login home.

A missing destination directory is confirmed and created; --mkdir
creates it without asking.`,
		Args:               zeroOrExactArgs(2, "pmox sync <source> <destination>", "pmox sync ./src/ web1:/opt/app/"),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, args, f, !noArchive, mkdir)
		},
	}
	addSSHFlags(cmd, f)
	cmd.Flags().BoolVar(&noArchive, "no-archive", false, "don't pass rsync -a (pass your own flags after --)")
	cmd.Flags().BoolVar(&mkdir, "mkdir", false, "create a missing destination directory without asking")
	return cmd
}

func runSync(cmd *cobra.Command, args []string, f *sshFlags, archive, mkdir bool) error {
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return fmt.Errorf("rsync binary not found on PATH; install rsync to use pmox sync")
	}

	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server

	localArg, remote, localIsSource, err := resolveCpSyncArgs(ctx, cmd, client, f, resolved.URL, srv, args, "pmox sync <source> <destination>", "pmox sync ./src/ web1:/opt/app/")
	if err != nil {
		return err
	}

	remote.vmRef, err = ensureVMRef(ctx, cmd, client, remote.vmRef)
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, remote.vmRef, f, resolved.URL, srv)
	if err != nil {
		return err
	}

	wholeDir := localIsSource && isLocalDir(localArg)
	if err := ensureTransferDir(ctx, cmd, target, localArg, remote.remotePath, localIsSource, wholeDir, mkdir); err != nil {
		return err
	}

	rsyncArgs := buildRsyncArgs(rsyncPath, target, localArg, remote.remotePath, localIsSource, archive, guestHostKeyOpts(), extraArgsAfterDash(cmd))
	return explainSSHFailure(ctx, target, resolved.URL, rsyncRunFn(rsyncPath, rsyncArgs), false)
}

func buildRsyncArgs(rsyncPath string, target *sshTarget, localPath, remotePath string, localIsSource, archive bool, hostKeyOpts, extra []string) []string {
	args := []string{rsyncPath}
	if archive {
		args = append(args, "-a") // copy directories, not "skipping directory"
	}
	args = append(args, "-e", rsyncSSHOption(target, hostKeyOpts))
	args = append(args, extra...)

	remoteSpec := fmt.Sprintf("%s@%s:%s", target.User, target.IP, remotePath)
	if localIsSource {
		args = append(args, localPath, remoteSpec)
	} else {
		args = append(args, remoteSpec, localPath)
	}
	return args
}
