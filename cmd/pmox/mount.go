package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

var defaultMountExcludes = []string{
	".git",
	".venv",
	".terraform",
	".terraform.*",
	"node_modules",
	"__pycache__",
	".DS_Store",
	"*.swp",
	"*.swo",
	"*~",
}

type mountFlags struct {
	sshFlags
	foreground  bool
	debounce    time.Duration
	noGitignore bool
	noDelete    bool
	excludes    []string
}

// mountResolveDestFn resolves a mount destination argument. If the
// arg has an explicit <name|vmid>:<remote_path> form it returns
// (ref, path) directly; otherwise it delegates VM resolution to the
// shared target picker and returns the picked VM's canonical name
// plus the raw arg as the remote path. Using the name (not vmid)
// keeps log lines, PID files, and daemon child args consistent with
// an explicit `<name>:<path>` invocation. Tests override this to
// bypass the picker/client plumbing.
var mountResolveDestFn = func(ctx context.Context, client *pveclient.Client, stderr io.Writer, dest string) (ref, remotePath string, err error) {
	if r, p, isRemote := parseRemoteArg(dest); isRemote {
		return r, p, nil
	}
	picked, err := vmPickFn(ctx, client)
	if err != nil {
		return "", "", err
	}
	return picked.Name, dest, nil
}

// mountRsyncRunFn runs rsync for mount. Tests override this.
var mountRsyncRunFn = func(bin string, args []string, stderr io.Writer) error {
	c := exec.Command(bin, args[1:]...)
	c.Stdout = stderr // rsync output goes to stderr
	c.Stderr = stderr
	return c.Run()
}

func newMountCmd() *cobra.Command {
	f := &mountFlags{}
	cmd := &cobra.Command{
		Use:   "mount <local_path> [<name|vmid>:]<remote_path>",
		Short: "Watch a local directory and continuously sync to a VM",
		Long: `Watch a local directory for filesystem changes and continuously
synchronize them to a pmox-managed VM using rsync over SSH.

The source is always a local directory. The destination may use
<name>:<path> syntax to pin a specific VM, or a bare <remote_path>
in which case pmox resolves the VM via the shared target picker
(auto-selecting when exactly one pmox VM exists, or prompting when
several do).

By default, pmox mount runs in the background, writes a PID file,
and streams sync activity to a log file. Pass --foreground / -F to
run attached to the terminal instead; stop background mounts with
pmox umount.

Default rsync flags: -az --partial --delete --filter=':- .gitignore'
plus built-in excludes (.git, node_modules, .venv, etc.).

Built-in default excludes (replaced by --exclude or config mount_excludes):
  .git  .venv  .terraform  .terraform.*  node_modules
  __pycache__  .DS_Store  *.swp  *.swo  *~

Examples:
  pmox mount ./src /opt/app
  pmox mount ./src web1:/opt/app
  pmox mount -F ./src web1:/opt/app
  pmox mount --no-delete --no-gitignore ./src web1:/opt/app
  pmox mount --exclude=.git --exclude='*.log' ./src web1:/opt/app
  pmox mount ./src web1:/opt/app -- --bwlimit=1000

On a terminal, 'pmox mount' alone prompts for both the local path and
the remote target (the latter falling back to the shared VM picker
when it has no <name|vmid>: prefix, same as when typed explicitly).`,
		Args: zeroOrExactArgs(2, "pmox mount <local_path> [<name|vmid>:]<remote_path>", "pmox mount ./src web1:/opt/app"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMount(cmd, args, f)
		},
	}
	addSSHFlags(cmd, &f.sshFlags)
	cmd.Flags().BoolVarP(&f.foreground, "foreground", "F", false, "run attached to the terminal instead of in the background")
	cmd.Flags().DurationVar(&f.debounce, "debounce", 300*time.Millisecond, "debounce duration for filesystem events")
	cmd.Flags().BoolVar(&f.noGitignore, "no-gitignore", false, "disable .gitignore filtering")
	cmd.Flags().BoolVar(&f.noDelete, "no-delete", false, "disable --delete from rsync")
	cmd.Flags().StringArrayVarP(&f.excludes, "exclude", "x", nil, "rsync exclude pattern (replaces defaults; repeatable)")
	return cmd
}

func newUmountCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "umount [<name|vmid>:<remote_path>]",
		Short: "Stop running daemon-mode mounts",
		Long: `Stop running daemon-mode mounts by finding their PID files and
sending SIGTERM to each process.

Called with no arguments, umount resolves the target VM via the
shared target picker (auto-selecting when exactly one pmox VM
exists, or prompting when several do) and stops every mount
associated with that VM — equivalent to pmox umount --all <vm>.

Examples:
  pmox umount
  pmox umount web1:/opt/app
  pmox umount --all web1`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUmount(cmd, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop all mounts for the given VM")
	return cmd
}

// resolveMountArgs returns mount's two positionals, prompting for
// whichever are missing when running interactively. cobra's Args check
// (zeroOrExactArgs) already guarantees len(args) is 0 or 2, so only the
// all-missing case needs handling; non-interactively that's still the
// same hard error mount always gave for a missing argument.
func resolveMountArgs(cmd *cobra.Command, args []string) (localPath, destArg string, err error) {
	if len(args) == 2 {
		return args[0], args[1], nil
	}
	if !tui.Interactive() || outputMode == "json" {
		return "", "", fmt.Errorf("%w: expected 2 arguments, got 0 — usage: pmox mount <local_path> [<name|vmid>:]<remote_path> (example: pmox mount ./src web1:/opt/app)", exitcode.ErrUserInput)
	}
	p := newStdPrompter(cmd.Context())
	if localPath, err = promptRequired(p, "Local path to sync: ", "a local path is required"); err != nil {
		return "", "", err
	}
	if destArg, err = promptRequired(p, "Remote target ([name|vmid:]path): ", "a remote target is required"); err != nil {
		return "", "", err
	}
	return localPath, destArg, nil
}

func runMount(cmd *cobra.Command, args []string, f *mountFlags) error {
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return fmt.Errorf("rsync binary not found on PATH; install rsync to use pmox mount")
	}

	localPath, destArg, err := resolveMountArgs(cmd, args)
	if err != nil {
		return err
	}

	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source directory %q not found", localPath)
		}
		return fmt.Errorf("stat source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a directory; pmox mount requires a directory", localPath)
	}

	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server

	ref, remotePath, err := mountResolveDestFn(ctx, client, cmd.ErrOrStderr(), destArg)
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, ref, &f.sshFlags, srv.User, srv.SSHPubkey)
	if err != nil {
		return err
	}

	excludes := resolveExcludes(f.excludes)
	rsyncArgs := buildMountRsyncArgs(rsyncPath, target, localPath, remotePath, f.noGitignore, f.noDelete, excludes, guestHostKeyOpts(), extraArgsAfterDash(cmd))

	stderr := cmd.ErrOrStderr()

	if !f.foreground {
		return runMountDaemon(cmd, localPath, ref, remotePath, resolved.URL, f)
	}

	fmt.Fprintf(stderr, "Syncing %s → %s:%s\n", localPath, ref, remotePath)

	if err := mountRsyncRunFn(rsyncPath, rsyncArgs, stderr); err != nil {
		return fmt.Errorf("initial rsync failed: %w", err)
	}
	fmt.Fprintf(stderr, "%s initial sync complete\n", timestamp())

	return watchAndSync(cmd, rsyncPath, rsyncArgs, localPath, f.debounce, stderr)
}

func resolveExcludes(flagExcludes []string) []string {
	if len(flagExcludes) > 0 {
		return flagExcludes
	}

	cfg, err := mountConfigLoadFn()
	if err == nil && len(cfg.MountExcludes) > 0 {
		return cfg.MountExcludes
	}

	return defaultMountExcludes
}

// mountConfigLoadFn loads config for mount_excludes. Tests override this.
var mountConfigLoadFn = config.Load

func buildMountRsyncArgs(rsyncPath string, target *sshTarget, localPath, remotePath string, noGitignore, noDelete bool, excludes, hostKeyOpts, extra []string) []string {
	args := []string{rsyncPath}
	args = append(args, "-e", rsyncSSHOption(target, hostKeyOpts))
	args = append(args, "-az", "--partial")

	if !noDelete {
		args = append(args, "--delete")
	}
	if !noGitignore {
		args = append(args, "--filter=:- .gitignore")
	}

	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}

	args = append(args, extra...)

	localTrailing := localPath
	if !strings.HasSuffix(localTrailing, "/") {
		localTrailing += "/"
	}
	remoteSpec := fmt.Sprintf("%s@%s:%s", target.User, target.IP, remotePath)
	args = append(args, localTrailing, remoteSpec)
	return args
}

func watchAndSync(cmd *cobra.Command, rsyncPath string, rsyncArgs []string, localPath string, debounce time.Duration, stderr io.Writer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := addWatchRecursive(watcher, localPath); err != nil {
		return fmt.Errorf("watch %s: %w", localPath, err)
	}

	fmt.Fprintf(stderr, "%s watching for changes...\n", timestamp())

	ctx := cmd.Context()
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			if pending {
				timer.Stop()
			}
			fmt.Fprintf(stderr, "%s shutting down, final sync...\n", timestamp())
			if err := mountRsyncRunFn(rsyncPath, rsyncArgs, stderr); err != nil {
				fmt.Fprintf(stderr, "%s final sync error: %v\n", timestamp(), err)
			} else {
				fmt.Fprintf(stderr, "%s final sync complete\n", timestamp())
			}
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					_ = addWatchRecursive(watcher, event.Name)
				}
			}

			if !pending {
				timer.Reset(debounce)
				pending = true
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if isOverflow(err) {
				fmt.Fprintf(stderr, "%s watcher overflow, running full sync...\n", timestamp())
				if syncErr := mountRsyncRunFn(rsyncPath, rsyncArgs, stderr); syncErr != nil {
					fmt.Fprintf(stderr, "%s sync error: %v\n", timestamp(), syncErr)
				} else {
					fmt.Fprintf(stderr, "%s full sync complete\n", timestamp())
				}
			} else {
				fmt.Fprintf(stderr, "%s watcher error: %v\n", timestamp(), err)
			}

		case <-timer.C:
			pending = false
			if syncErr := mountRsyncRunFn(rsyncPath, rsyncArgs, stderr); syncErr != nil {
				fmt.Fprintf(stderr, "%s sync error: %v\n", timestamp(), syncErr)
			} else {
				fmt.Fprintf(stderr, "%s synced\n", timestamp())
			}
		}
	}
}

func isOverflow(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "overflow") || errors.Is(err, fsnotify.ErrEventOverflow))
}

func addWatchRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if err := w.Add(path); err != nil {
				return fmt.Errorf("watch %s: %w", path, err)
			}
		}
		return nil
	})
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

// --- Daemon mode ---

// mountStartDaemonFn spawns and records the detached mount daemon.
// Tests override this to capture the child argv without forking.
var mountStartDaemonFn = mount.Spawn

// mountChildArgs builds the argv for the detached daemon. The child
// runs `pmox mount --foreground` with the parent's flags, and is pinned
// to the server the parent already resolved via --server <URL> so it
// cannot pick a different (or ambiguous) server — e.g. when the parent
// was targeted with --context. Global flags go before the positional
// args so they never land after `--` among the rsync pass-through args.
func mountChildArgs(exe string, f *mountFlags, serverURL, localPath, vmName, remotePath string, insecure bool, extra []string) []string {
	args := []string{exe, "mount", "--foreground"}
	if serverURL != "" {
		args = append(args, "--server", serverURL)
	}
	if f.user != "" {
		args = append(args, "--user", f.user)
	}
	if f.identity != "" {
		args = append(args, "--identity", f.identity)
	}
	if f.force {
		args = append(args, "--force")
	}
	if insecure {
		// Propagate the host-key mode so the detached daemon uses the
		// same verification behavior the operator chose for the parent.
		args = append(args, "--ssh-insecure")
	}
	if f.noGitignore {
		args = append(args, "--no-gitignore")
	}
	if f.noDelete {
		args = append(args, "--no-delete")
	}
	if f.debounce != 300*time.Millisecond {
		args = append(args, "--debounce", f.debounce.String())
	}
	for _, ex := range f.excludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, localPath, fmt.Sprintf("%s:%s", vmName, remotePath))
	if len(extra) > 0 {
		args = append(args, "--")
		args = append(args, extra...)
	}
	return args
}

func runMountDaemon(cmd *cobra.Command, localPath, vmName, remotePath, serverURL string, f *mountFlags) error {
	stateDir, err := mount.StateDir()
	if err != nil {
		return fmt.Errorf("resolve mount state dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// If a record already exists for this exact local→remote pair and its
	// daemon is still live, refuse to start a duplicate. A stale record
	// (dead or recycled pid) is cleared so the new daemon can take over.
	if rec, ok, err := mount.Find(stateDir, localPath, remotePath); err == nil && ok {
		if rec.Live() {
			return fmt.Errorf("mount already active (pid %d) for %s → %s:%s", rec.PID, localPath, vmName, remotePath)
		}
		_ = mount.Remove(rec)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	childArgs := mountChildArgs(exe, f, serverURL, localPath, vmName, remotePath, sshInsecure, extraArgsAfterDash(cmd))
	logPath := mount.LogPath(stateDir, vmName, localPath, remotePath)
	rec, err := mountStartDaemonFn(stateDir, exe, childArgs, mount.Record{
		VMName:     vmName,
		LocalPath:  localPath,
		RemotePath: remotePath,
		LogPath:    logPath,
	})
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	fmt.Fprintf(stderr, "mount started in background (pid %d)\n", rec.PID)
	fmt.Fprintf(stderr, "  %s → %s:%s\n", localPath, vmName, remotePath)
	fmt.Fprintf(stderr, "  logs: %s\n", logPath)
	fmt.Fprintf(stderr, "  stop with: pmox umount %s:%s\n", vmName, remotePath)
	return nil
}

// --- Umount command ---

// umountResolveVMFn resolves the target VM when umount is invoked with
// no positional arguments. It builds the SSH client and picks a VM,
// returning the canonical VM name so umountAll's PID-file prefix
// lookup matches the names mount uses. Tests override this to return
// a fixed VM name and skip the client/config plumbing.
var umountResolveVMFn = func(cmd *cobra.Command) (string, error) {
	ctx := cmd.Context()
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return "", err
	}
	picked, err := vmPickFn(ctx, client)
	if err != nil {
		return "", err
	}
	return picked.Name, nil
}

// umountGrace is how long a mount daemon is given to shut down
// gracefully after SIGTERM before it is force-killed.
const umountGrace = 10 * time.Second

func runUmount(cmd *cobra.Command, args []string, all bool) error {
	if len(args) == 0 {
		vmName, err := umountResolveVMFn(cmd)
		if err != nil {
			return err
		}
		if err := umountAll(cmd, vmName); err != nil {
			if errors.Is(err, errNoMountsFound) {
				fmt.Fprintln(cmd.ErrOrStderr(), colorize(fmt.Sprintf("No active mounts for %s", vmName), colorGreen))
				return nil
			}
			return err
		}
		return nil
	}

	arg := args[0]
	if all {
		vmName := strings.TrimSuffix(arg, ":")
		ref, _, isRemote := parseRemoteArg(arg)
		if isRemote {
			vmName = ref
		}
		return umountAll(cmd, vmName)
	}

	ref, remotePath, isRemote := parseRemoteArg(arg)
	if !isRemote {
		return fmt.Errorf("argument must use <name>:<path> syntax (e.g. web1:/opt/app)")
	}
	return umountByRemote(cmd, ref, remotePath)
}

// stopRecord stops the daemon behind a record and removes the record.
// A record whose process is already gone is treated as stale: removed
// with a note, reported as not-stopped.
func stopRecord(cmd *cobra.Command, rec mount.Record) (stopped bool) {
	if !rec.Live() {
		// Either the process is gone, or the pid is alive but belongs to
		// some other program — recycled since this record was written
		// (e.g. across a reboot). Drop the record instead of signalling
		// an unrelated process.
		_ = mount.Remove(rec)
		reason := "process not running"
		if mount.Alive(rec.PID) {
			reason = fmt.Sprintf("pid %d now belongs to another process", rec.PID)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "removed stale mount record for %s:%s (%s)\n", rec.VMName, rec.RemotePath, reason)
		return false
	}
	killed, err := mount.Stop(rec.PID, umountGrace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "failed to stop pid %d: %v\n", rec.PID, err)
		return false
	}
	_ = mount.Remove(rec)
	if killed {
		fmt.Fprintf(cmd.ErrOrStderr(), "mount (pid %d) did not exit within %s; sent SIGKILL\n", rec.PID, umountGrace)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "stopped mount (pid %d) %s → %s:%s\n", rec.PID, rec.LocalPath, rec.VMName, rec.RemotePath)
	return true
}

// umountByRemote stops the single mount matching a VM name AND remote
// path. The registry records both endpoints, so this targets exactly one
// mount instead of every mount for the VM.
func umountByRemote(cmd *cobra.Command, vmName, remotePath string) error {
	records, err := mountRecordsForVM(vmName)
	if err != nil {
		return err
	}
	found := false
	for _, rec := range records {
		if rec.RemotePath != remotePath {
			continue
		}
		if stopRecord(cmd, rec) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no mount found for %s:%s", vmName, remotePath)
	}
	return nil
}

const colorGreen = "\033[32m"

// colorize wraps s in an ANSI color escape when stderr is a TTY.
// Non-TTY output stays plain so scripts and log files see clean text.
func colorize(s, color string) string {
	if !tui.StderrIsTerminal() {
		return s
	}
	return color + s + "\033[0m"
}

// errNoMountsFound signals an empty umountAll — the state dir exists
// but nothing matched the VM prefix (or the state dir does not exist
// yet). It is a "nothing to do" outcome, not a real failure, so the
// zero-arg `pmox umount` branch surfaces it as friendly info instead
// of an error. The `--all <vm>` branch still propagates it as a
// regular error to preserve scripted behavior.
var errNoMountsFound = errors.New("no mounts found")

// mountRecordsForVM returns the mount records for vmName from the mount
// state dir.
func mountRecordsForVM(vmName string) ([]mount.Record, error) {
	stateDir, err := mount.StateDir()
	if err != nil {
		return nil, fmt.Errorf("resolve mount state dir: %w", err)
	}
	records, err := mount.ForVM(stateDir, vmName)
	if err != nil {
		return nil, fmt.Errorf("read mount records: %w", err)
	}
	return records, nil
}

func umountAll(cmd *cobra.Command, vmName string) error {
	records, err := mountRecordsForVM(vmName)
	if err != nil {
		return err
	}
	stopped := 0
	for _, rec := range records {
		if stopRecord(cmd, rec) {
			stopped++
		}
	}
	if stopped == 0 {
		return fmt.Errorf("%w for %s", errNoMountsFound, vmName)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "stopped %d mount(s) for %s\n", stopped, vmName)
	return nil
}
