package mount

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStateDirHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "pmox", "mounts"); got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
}

func TestLogPath(t *testing.T) {
	dir := "/state"
	first, second := LogPath(dir, "web1", "/a", "/b"), LogPath(dir, "web1", "/a", "/b")
	if first != second {
		t.Error("LogPath must be deterministic")
	}
	if LogPath(dir, "web1", "/a", "/b") == LogPath(dir, "web2", "/a", "/b") {
		t.Error("different VMs must yield different logs")
	}
	p := LogPath(dir, "evil/../vm", "/a", "/b")
	if filepath.Dir(p) != dir {
		t.Errorf("LogPath escaped state dir: %q", p)
	}
	base := filepath.Base(p)
	if !strings.HasPrefix(base, "evil_.._vm-") || !strings.HasSuffix(base, ".log") {
		t.Errorf("unexpected log name %q", base)
	}
}

func TestRecordLive(t *testing.T) {
	orig := readProcessCmd
	t.Cleanup(func() { readProcessCmd = orig })

	self := Record{PID: os.Getpid()}

	readProcessCmd = func(int) (string, error) { return "/usr/local/bin/pmox mount --foreground", nil }
	if !self.Live() {
		t.Error("alive pmox pid: Live() = false, want true")
	}

	readProcessCmd = func(int) (string, error) { return "/usr/bin/vim", nil }
	if self.Live() {
		t.Error("alive but reused pid: Live() = true, want false")
	}

	readProcessCmd = func(int) (string, error) { return "", errors.New("ps failed") }
	if !self.Live() {
		t.Error("alive pid with inconclusive probe: Live() = false, want true")
	}

	readProcessCmd = func(int) (string, error) { return "pmox", nil }
	if (Record{PID: 0}).Live() {
		t.Error("pid 0: Live() = true, want false")
	}
	if (Record{PID: 2147480000}).Live() {
		t.Error("dead pid: Live() = true, want false")
	}
}

// If the record cannot be saved after the daemon is started, the daemon
// must be killed rather than left running untracked.
func TestSpawn_SaveFailureKillsChild(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}
	origSave := saveRecord
	t.Cleanup(func() { saveRecord = origSave })
	saveRecord = func(string, Record) (Record, error) {
		return Record{}, errors.New("disk full")
	}

	dir := t.TempDir()
	rec := Record{VMName: "web1", LocalPath: "/a", RemotePath: "/b", LogPath: filepath.Join(dir, "web1.log")}
	got, err := Spawn(dir, sleepBin, []string{sleepBin, "30"}, rec)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Spawn err = %v, want record failure", err)
	}
	if got.PID <= 0 {
		t.Fatalf("PID not set on failure: %+v", got)
	}
	t.Cleanup(func() { _ = syscall.Kill(got.PID, syscall.SIGKILL) })
	// Spawn kills and reaps the child, so it must be gone immediately.
	deadline := time.Now().Add(2 * time.Second)
	for Alive(got.PID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if Alive(got.PID) {
		t.Errorf("child pid %d still alive after Save failure", got.PID)
	}
	if recs, _ := List(dir); len(recs) != 0 {
		t.Errorf("unexpected records: %+v", recs)
	}
}

func TestSpawn_RecordsDaemon(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}
	var gotArgs []string
	origStart := startProcess
	t.Cleanup(func() { startProcess = origStart })
	startProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		gotArgs = argv
		if attr.Sys == nil || !attr.Sys.Setsid {
			t.Error("daemon must be started in a new session (Setsid)")
		}
		return origStart(name, argv, attr)
	}

	dir := t.TempDir()
	rec := Record{VMName: "web1", LocalPath: "/a", RemotePath: "/b", LogPath: filepath.Join(dir, "web1.log")}
	got, err := Spawn(dir, sleepBin, []string{sleepBin, "30"}, rec)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(got.PID, syscall.SIGKILL) })
	if len(gotArgs) != 2 || gotArgs[1] != "30" {
		t.Errorf("argv = %v", gotArgs)
	}
	found, ok, err := Find(dir, "/a", "/b")
	if err != nil || !ok {
		t.Fatalf("Find: ok=%v err=%v", ok, err)
	}
	if found.PID != got.PID || found.LogPath != rec.LogPath {
		t.Errorf("record = %+v, want pid %d", found, got.PID)
	}
	if _, err := os.Stat(rec.LogPath); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}
