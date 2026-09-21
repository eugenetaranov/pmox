package mount

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// pollInterval is how often Stop re-checks whether a signalled process
// has exited during the graceful window.
const pollInterval = 100 * time.Millisecond

// Alive reports whether pid refers to a live process. A background mount
// daemon is started detached (setsid + Release), so it is NOT a child of
// the umount process — os.Process.Wait cannot observe it. Signal 0 is
// the correct liveness probe: it delivers nothing but still returns an
// error (ESRCH) when the process is gone.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Stop terminates a background mount daemon. It sends SIGTERM, then
// polls (via Signal 0) up to graceful for the process to exit — because
// the daemon is not our child, Wait() would return immediately with
// "no child processes" and never enforce the timeout. If the process is
// still alive after the graceful window it is SIGKILLed. Returns true
// when a SIGKILL was needed.
//
// A process that is already gone is treated as a successful stop.
func Stop(pid int, graceful time.Duration) (killed bool, err error) {
	if !Alive(pid) {
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// ESRCH means it exited between the Alive check and now.
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}

	deadline := time.Now().Add(graceful)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return false, nil
		}
		time.Sleep(pollInterval)
	}
	if !Alive(pid) {
		return false, nil
	}
	_ = proc.Signal(syscall.SIGKILL)
	return true, nil
}
