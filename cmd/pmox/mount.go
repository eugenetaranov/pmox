package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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
	picked, err := vmPickFn(ctx, client, stderr)
	if err != nil {
		return "", "", err
	}
	return picked.Name, dest, nil
}

// mountRsyncRunFn runs rsync for mount. Tests override this.
var mountRsyncRunFn = func(bin string, args []string, stderr *os.File) error {
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
  pmox mount ./src web1:/opt/app -- --bwlimit=1000`,
		Args: cobra.ExactArgs(2),
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

func runMount(cmd *cobra.Command, args []string, f *mountFlags) error {
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return fmt.Errorf("rsync binary not found on PATH; install rsync to use pmox mount")
	}

	localPath := args[0]
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
	client, srv, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return err
	}

	ref, remotePath, err := mountResolveDestFn(ctx, client, cmd.ErrOrStderr(), args[1])
	if err != nil {
		return err
	}

	target, err := resolveSSHTarget(ctx, cmd, client, ref, &f.sshFlags, srv.User, srv.SSHPubkey)
	if err != nil {
		return err
	}

	excludes := resolveExcludes(f.excludes)
	rsyncArgs := buildMountRsyncArgs(rsyncPath, target, localPath, remotePath, f.noGitignore, f.noDelete, excludes, extraArgsAfterDash())

	stderr := os.Stderr

	if !f.foreground {
		return runMountDaemon(cmd, rsyncPath, rsyncArgs, localPath, ref, remotePath, f, target, excludes)
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

	cfg, err := loadMountConfig()
	if err == nil && len(cfg) > 0 {
		return cfg
	}

	return defaultMountExcludes
}

func loadMountConfig() ([]string, error) {
	cfg, err := configLoadFn()
	if err != nil {
		return nil, err
	}
	return cfg.MountExcludes, nil
}

// configLoadFn loads config. Tests override this.
var configLoadFn = loadConfigForMount

func loadConfigForMount() (*mountConfig, error) {
	raw, err := os.ReadFile(configPathForMount())
	if err != nil {
		return nil, err
	}
	// Quick parse just the mount_excludes field
	var mc mountConfig
	if err := yamlUnmarshalFn(raw, &mc); err != nil {
		return nil, err
	}
	return &mc, nil
}

type mountConfig struct {
	MountExcludes []string `yaml:"mount_excludes"`
}

var yamlUnmarshalFn = func(data []byte, v interface{}) error {
	return yaml.Unmarshal(data, v)
}

func configPathForMount() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "pmox", "config.yaml")
}

func buildMountRsyncArgs(rsyncPath string, target *sshTarget, localPath, remotePath string, noGitignore, noDelete bool, excludes, extra []string) []string {
	args := []string{rsyncPath}
	args = append(args, "-e", sshOptionString(target))
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

func watchAndSync(cmd *cobra.Command, rsyncPath string, rsyncArgs []string, localPath string, debounce time.Duration, stderr *os.File) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

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
	return err != nil && strings.Contains(err.Error(), "overflow") || err == fsnotify.ErrEventOverflow
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

func mountStateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "mounts")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "pmox", "mounts")
}

func pidFilePath(vmName, localPath, remotePath string) string {
	h := sha256.Sum256([]byte(localPath + "\x00" + remotePath))
	return filepath.Join(mountStateDir(), fmt.Sprintf("%s-%x.pid", vmName, h[:8]))
}

func logFilePath(vmName, localPath, remotePath string) string {
	h := sha256.Sum256([]byte(localPath + "\x00" + remotePath))
	return filepath.Join(mountStateDir(), fmt.Sprintf("%s-%x.log", vmName, h[:8]))
}

func runMountDaemon(cmd *cobra.Command, rsyncPath string, rsyncArgs []string, localPath, vmName, remotePath string, f *mountFlags, target *sshTarget, excludes []string) error {
	stateDir := mountStateDir()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	pidPath := pidFilePath(vmName, localPath, remotePath)

	if data, err := os.ReadFile(pidPath); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			if process, err := os.FindProcess(pid); err == nil {
				if err := process.Signal(syscall.Signal(0)); err == nil {
					return fmt.Errorf("mount already active (pid %d) for %s → %s:%s", pid, localPath, vmName, remotePath)
				}
			}
			// Stale PID file
			os.Remove(pidPath)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	// Build the child command args — the child runs in foreground
	// so the parent can daemonize it via StartProcess + Setsid.
	childArgs := []string{exe, "mount", "--foreground"}
	if f.user != "pmox" {
		childArgs = append(childArgs, "--user", f.user)
	}
	if f.identity != "" {
		childArgs = append(childArgs, "--identity", f.identity)
	}
	if f.force {
		childArgs = append(childArgs, "--force")
	}
	if f.noGitignore {
		childArgs = append(childArgs, "--no-gitignore")
	}
	if f.noDelete {
		childArgs = append(childArgs, "--no-delete")
	}
	if f.debounce != 300*time.Millisecond {
		childArgs = append(childArgs, "--debounce", f.debounce.String())
	}
	for _, ex := range f.excludes {
		childArgs = append(childArgs, "--exclude="+ex)
	}
	childArgs = append(childArgs, localPath, fmt.Sprintf("%s:%s", vmName, remotePath))
	if extra := extraArgsAfterDash(); len(extra) > 0 {
		childArgs = append(childArgs, "--")
		childArgs = append(childArgs, extra...)
	}

	// Also pass global flags
	if serverFlag != "" {
		childArgs = append(childArgs, "--server", serverFlag)
	}

	logPath := logFilePath(vmName, localPath, remotePath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()

	proc, err := os.StartProcess(exe, childArgs, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{devNull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("start background process: %w", err)
	}
	pid := proc.Pid

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}

	if err := proc.Release(); err != nil {
		return fmt.Errorf("release process: %w", err)
	}

	fmt.Fprintf(os.Stderr, "mount started in background (pid %d)\n", pid)
	fmt.Fprintf(os.Stderr, "  %s → %s:%s\n", localPath, vmName, remotePath)
	fmt.Fprintf(os.Stderr, "  logs: %s\n", logPath)
	fmt.Fprintf(os.Stderr, "  stop with: pmox umount %s:%s\n", vmName, remotePath)
	return nil
}

func cleanPIDFileOnShutdown(localPath, vmName, remotePath string) {
	pidPath := pidFilePath(vmName, localPath, remotePath)
	os.Remove(pidPath)
}

// --- Umount command ---

// umountResolveVMFn resolves the target VM when umount is invoked with
// no positional arguments. It builds the SSH client and picks a VM,
// returning the canonical VM name so umountAll's PID-file prefix
// lookup matches the names mount uses. Tests override this to return
// a fixed VM name and skip the client/config plumbing.
var umountResolveVMFn = func(cmd *cobra.Command) (string, error) {
	ctx := cmd.Context()
	client, _, err := buildSSHClient(ctx, cmd)
	if err != nil {
		return "", err
	}
	picked, err := vmPickFn(ctx, client, cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}
	return picked.Name, nil
}

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

	stateDir := mountStateDir()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return fmt.Errorf("no mount found for %s:%s", ref, remotePath)
	}

	prefix := ref + "-"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		pidPath := filepath.Join(stateDir, e.Name())
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid <= 0 {
			os.Remove(pidPath)
			continue
		}

		// We can't know for certain which PID file matches without storing more metadata.
		// Use the hash-based path to find the exact match.
		break
	}

	// Try to find the exact PID file by scanning all local paths
	// The PID file path requires knowledge of the local path, which we don't have from umount.
	// Instead, search for all PID files for this VM and match by reading them.
	// Since the hash includes local+remote, and we only know remote, we check all.
	return umountByRemote(cmd, ref, remotePath)
}

func umountByRemote(cmd *cobra.Command, vmName, remotePath string) error {
	stateDir := mountStateDir()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return fmt.Errorf("no mount found for %s:%s", vmName, remotePath)
	}

	prefix := vmName + "-"
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		pidPath := filepath.Join(stateDir, e.Name())
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid <= 0 {
			os.Remove(pidPath)
			continue
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			os.Remove(pidPath)
			fmt.Fprintf(cmd.ErrOrStderr(), "removed stale PID file %s\n", e.Name())
			continue
		}

		if err := process.Signal(syscall.Signal(0)); err != nil {
			os.Remove(pidPath)
			fmt.Fprintf(cmd.ErrOrStderr(), "removed stale PID file %s (process not running)\n", e.Name())
			continue
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal pid %d: %w", pid, err)
		}

		// Wait for process to exit (with timeout)
		done := make(chan struct{})
		go func() {
			process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			fmt.Fprintf(cmd.ErrOrStderr(), "process %d did not exit within 10s, sending SIGKILL\n", pid)
			process.Signal(syscall.SIGKILL)
		}

		os.Remove(pidPath)
		fmt.Fprintf(cmd.ErrOrStderr(), "stopped mount (pid %d)\n", pid)
		found = true
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

func umountAll(cmd *cobra.Command, vmName string) error {
	stateDir := mountStateDir()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return fmt.Errorf("%w for %s", errNoMountsFound, vmName)
	}

	prefix := vmName + "-"
	stopped := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		pidPath := filepath.Join(stateDir, e.Name())
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid <= 0 {
			os.Remove(pidPath)
			continue
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			os.Remove(pidPath)
			continue
		}

		if err := process.Signal(syscall.Signal(0)); err != nil {
			os.Remove(pidPath)
			fmt.Fprintf(cmd.ErrOrStderr(), "removed stale PID file %s\n", e.Name())
			continue
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "failed to signal pid %d: %v\n", pid, err)
			continue
		}

		done := make(chan struct{})
		go func() {
			process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			process.Signal(syscall.SIGKILL)
		}

		os.Remove(pidPath)
		fmt.Fprintf(cmd.ErrOrStderr(), "stopped mount (pid %d)\n", pid)
		stopped++
	}

	if stopped == 0 {
		return fmt.Errorf("%w for %s", errNoMountsFound, vmName)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "stopped %d mount(s) for %s\n", stopped, vmName)
	return nil
}
