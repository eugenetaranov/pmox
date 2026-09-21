package mount

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startDetached launches inner as a background job under a short-lived
// `sh` that exits immediately, so the target reparents to init and is
// NOT a child of the test process. This reproduces production, where
// `pmox umount` is never the mount daemon's parent — so Signal(0)
// liveness reflects reality and a dead process is reaped (no zombie
// masking). Returns the target's PID (from `echo $!`).
func startDetached(t *testing.T, inner string) int {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	script := inner + " >/dev/null 2>&1 & echo $!"
	out, err := exec.Command(sh, "-c", script).Output()
	if err != nil {
		t.Fatalf("start detached %q: %v", inner, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		t.Fatalf("bad pid from detached start: %q", out)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGKILL)
		}
	})
	for i := 0; i < 50 && !Alive(pid); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return pid
}

func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after %s", pid, within)
}

func TestAlive(t *testing.T) {
	if Alive(-1) || Alive(0) {
		t.Error("non-positive pids must not be alive")
	}
	pid := startDetached(t, "sleep 30")
	if !Alive(pid) {
		t.Fatalf("freshly started process %d should be alive", pid)
	}
}

func TestStop_GracefulExit(t *testing.T) {
	// sleep terminates on SIGTERM (default disposition), so Stop should
	// succeed without resorting to SIGKILL.
	pid := startDetached(t, "sleep 30")
	killed, err := Stop(pid, 5*time.Second)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if killed {
		t.Error("expected graceful SIGTERM exit, not SIGKILL")
	}
	waitGone(t, pid, 2*time.Second)
}

func TestStop_SIGKILLAfterGrace(t *testing.T) {
	// A process that ignores SIGTERM must be SIGKILLed after the grace
	// window — this is the path that was previously unreachable because
	// Wait() on a non-child returned instantly.
	pid := startDetached(t, `sh -c 'trap "" TERM; sleep 30; :'`)
	// Give the shell a moment to actually execute `trap` before we
	// signal it; otherwise a SIGTERM racing shell startup hits the
	// default disposition and kills it (a test-only race — in production
	// umount runs long after the daemon has started).
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	killed, err := Stop(pid, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !killed {
		t.Error("expected SIGKILL for a process ignoring SIGTERM")
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("Stop returned in %v, before the grace window elapsed", elapsed)
	}
	waitGone(t, pid, 2*time.Second)
}

func TestStop_AlreadyGone(t *testing.T) {
	pid := startDetached(t, "sleep 30")
	if p, _ := os.FindProcess(pid); p != nil {
		_ = p.Signal(syscall.SIGKILL)
	}
	waitGone(t, pid, 2*time.Second)
	killed, err := Stop(pid, time.Second)
	if err != nil {
		t.Fatalf("Stop on dead pid should not error: %v", err)
	}
	if killed {
		t.Error("already-dead process should not report a kill")
	}
}
