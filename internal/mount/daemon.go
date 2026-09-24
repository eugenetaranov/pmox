package mount

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/eugenetaranov/pmox/internal/paths"
)

// StateDir returns the directory holding mount records and daemon logs:
// <pmox state dir>/mounts (XDG_STATE_HOME-aware).
func StateDir() (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mounts"), nil
}

// LogPath is the daemon log file for a mount under stateDir. Like the
// sidecar name it is keyed by the (sanitized) VM name plus the path-pair
// ID, so re-mounting the same pair appends to the same log.
func LogPath(stateDir, vmName, localPath, remotePath string) string {
	return filepath.Join(stateDir, fmt.Sprintf("%s-%s.log", sanitize(vmName), ID(localPath, remotePath)))
}

// Live reports whether the record's daemon is still running: its PID is
// alive AND has not been recycled by an unrelated process.
func (r Record) Live() bool {
	return Alive(r.PID) && !LooksReused(r.PID)
}

// Seams for tests.
var (
	startProcess = os.StartProcess
	saveRecord   = Save
)

// Spawn starts a detached background daemon (new session via setsid,
// stdin from /dev/null, stdout+stderr appended to rec.LogPath), records
// it under stateDir, and releases the process handle. args is the full
// argv including args[0]. rec.PID is filled in from the started process.
//
// If the record cannot be saved the freshly started daemon is killed,
// so no untracked daemon is left running that umount could never find.
func Spawn(stateDir, exe string, args []string, rec Record) (Record, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return rec, fmt.Errorf("create state dir: %w", err)
	}
	logFile, err := os.OpenFile(rec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return rec, fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return rec, fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()

	proc, err := startProcess(exe, args, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{devNull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return rec, fmt.Errorf("start background process: %w", err)
	}
	rec.PID = proc.Pid

	saved, err := saveRecord(stateDir, rec)
	if err != nil {
		// Still our child (not yet released): kill and reap it.
		killErr := proc.Kill()
		_, _ = proc.Wait()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return rec, fmt.Errorf("record mount: %w (and failed to stop pid %d: %v)", err, rec.PID, killErr)
		}
		return rec, fmt.Errorf("record mount: %w", err)
	}

	if err := proc.Release(); err != nil {
		return saved, fmt.Errorf("release process: %w", err)
	}
	return saved, nil
}
