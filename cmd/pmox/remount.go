package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/mount"
)

// mountOptions is what a mount record keeps of how it was started.
func mountOptions(f *mountFlags, serverURL string, insecure bool, extra []string) *mount.Options {
	o := &mount.Options{
		Server: serverURL, User: f.user, Identity: f.identity, Force: f.force, Insecure: insecure,
		Gitignore: f.gitignore && !f.noGitignore, NoDelete: f.noDelete, Excludes: f.excludes, Extra: extra,
	}
	if f.debounce != 0 && f.debounce != 300*time.Millisecond {
		o.Debounce = f.debounce.String()
	}
	return o
}

// flagsFromOptions turns a record's options back into mount's flags.
func flagsFromOptions(o *mount.Options) *mountFlags {
	f := &mountFlags{gitignore: o.Gitignore, noDelete: o.NoDelete, excludes: o.Excludes, debounce: 300 * time.Millisecond}
	f.user, f.identity, f.force = o.User, o.Identity, o.Force
	if d, err := time.ParseDuration(o.Debounce); err == nil && d > 0 {
		f.debounce = d
	}
	return f
}

// restartMount stops rec's daemon if it runs and starts it again with
// opts (the record's, with any changes), under the pmox now installed.
func restartMount(rec mount.Record, opts *mount.Options) (mount.Record, error) {
	if !filepath.IsAbs(rec.LocalPath) {
		return rec, fmt.Errorf("%s:%s was mounted from %q, a relative path: stop it with 'pmox umount %s:%s' and mount it again", rec.VMName, rec.RemotePath, rec.LocalPath, rec.VMName, rec.RemotePath)
	}
	if err := checkMountSource(rec.LocalPath); err != nil {
		return rec, err
	}
	if rec.Live() {
		if _, err := mount.Stop(rec.PID, umountGrace); err != nil {
			return rec, fmt.Errorf("stop mount (pid %d): %w", rec.PID, err)
		}
	}
	_ = mount.Remove(rec)
	stateDir, err := mount.StateDir()
	if err != nil {
		return rec, err
	}
	exe, err := os.Executable()
	if err != nil {
		return rec, fmt.Errorf("find executable: %w", err)
	}
	args := mountChildArgs(exe, flagsFromOptions(opts), opts.Server, rec.LocalPath, rec.VMName, rec.RemotePath, opts.Insecure, opts.Extra)
	logPath := rec.LogPath
	if logPath == "" {
		logPath = mount.LogPath(stateDir, rec.VMName, rec.LocalPath, rec.RemotePath)
	}
	return mountStartDaemonFn(stateDir, exe, args, mount.Record{
		VMName: rec.VMName, LocalPath: rec.LocalPath, RemotePath: rec.RemotePath, LogPath: logPath, Options: opts,
	})
}

type remountFlags struct {
	stale     bool
	gitignore bool
	noDelete  bool
	debounce  time.Duration
	excludes  []string
	user      string
	identity  string
}

func newRemountCmd() *cobra.Command {
	f := &remountFlags{}
	cmd := &cobra.Command{
		Use:   "remount [<name|vmid>:<remote_path>] [flags] [-- <rsync args>]",
		Short: "Restart background mounts, with changed options",
		Long: `Stop background mounts and start them again: with the options they were
started with, changed by the flags given here (--exclude replaces the
list; -- <rsync args> replaces those). It also picks up a new pmox
version and changed mount_excludes in the config.

With a <vm>:<path>, that mount; without, every mount (--stale: only the
ones whose process isn't running, e.g. after a reboot).`,
		Example: `  pmox remount text2cad:~/projects/text2cad --exclude=.venv --exclude=data
  pmox remount --gitignore=false
  pmox remount --stale`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRemount(cmd, positionalArgs(cmd, args), f) },
	}
	cmd.Flags().BoolVar(&f.stale, "stale", false, "only the mounts whose process isn't running")
	cmd.Flags().BoolVar(&f.gitignore, "gitignore", false, "leave out what .gitignore lists (--gitignore=false: sync it)")
	cmd.Flags().BoolVar(&f.noDelete, "no-delete", false, "keep files on the VM that are gone locally (--no-delete=false: delete them)")
	cmd.Flags().DurationVar(&f.debounce, "debounce", 300*time.Millisecond, "debounce duration for filesystem events")
	cmd.Flags().StringArrayVarP(&f.excludes, "exclude", "x", nil, "rsync exclude pattern (replaces the mount's list; repeatable)")
	cmd.Flags().StringVarP(&f.user, "user", "u", "", "SSH login user")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH private key")
	return cmd
}

// applyRemountFlags changes opts by the flags given on the command line.
func applyRemountFlags(cmd *cobra.Command, f *remountFlags, o *mount.Options) {
	ch := cmd.Flags().Changed
	if ch("gitignore") {
		o.Gitignore = f.gitignore
	}
	if ch("no-delete") {
		o.NoDelete = f.noDelete
	}
	if ch("debounce") {
		o.Debounce = f.debounce.String()
	}
	if ch("exclude") {
		o.Excludes = f.excludes
	}
	if ch("user") {
		o.User = f.user
	}
	if ch("identity") {
		o.Identity = f.identity
	}
	if cmd.ArgsLenAtDash() >= 0 {
		o.Extra = extraArgsAfterDash(cmd)
	}
}

func runRemount(cmd *cobra.Command, args []string, f *remountFlags) error {
	stateDir, err := mount.StateDir()
	if err != nil {
		return err
	}
	all, err := mount.List(stateDir)
	if err != nil {
		return err
	}
	var recs []mount.Record
	if len(args) == 1 {
		vmName, remotePath, ok := parseRemoteArg(args[0])
		if !ok {
			return fmt.Errorf("%w: argument must use <name>:<path> syntax (e.g. web1:/opt/app)", exitcode.ErrUserInput)
		}
		for _, r := range all {
			if r.VMName == vmName && r.RemotePath == remotePath {
				recs = append(recs, r)
			}
		}
		if len(recs) == 0 {
			return fmt.Errorf("no mount for %s:%s (pmox mount list)", vmName, remotePath)
		}
	} else {
		for _, r := range all {
			if !f.stale || !r.Live() {
				recs = append(recs, r)
			}
		}
		if len(recs) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "no mounts to restart")
			return nil
		}
	}
	stderr := cmd.ErrOrStderr()
	var failed []string
	for _, r := range recs {
		name := fmt.Sprintf("%s → %s:%s", r.LocalPath, r.VMName, r.RemotePath)
		if r.Options == nil {
			fmt.Fprintf(stderr, "%s%s: started by an older pmox, which didn't record its options; mount it again\n", doneMark(false), name)
			failed = append(failed, r.VMName+":"+r.RemotePath)
			continue
		}
		opts := *r.Options
		applyRemountFlags(cmd, f, &opts)
		rec, err := restartMount(r, &opts)
		if err != nil {
			fmt.Fprintf(stderr, "%s%s: %v\n", doneMark(false), name, err)
			failed = append(failed, r.VMName+":"+r.RemotePath)
			continue
		}
		fmt.Fprintf(stderr, "%s%s (pid %d)\n", doneMark(true), name, rec.PID)
	}
	if len(failed) > 0 {
		return errors.New("not restarted: " + strings.Join(failed, " "))
	}
	return nil
}
