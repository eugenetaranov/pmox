package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/mount"
)

func TestMountOptionsRoundTrip(t *testing.T) {
	f := &mountFlags{gitignore: true, noDelete: true, excludes: []string{"data"}, debounce: time.Second}
	f.user, f.identity = "ubuntu", "/k"
	o := mountOptions(f, "https://pve:8006/api2/json", true, []string{"--bwlimit=100"})
	back := flagsFromOptions(o)
	if !back.gitignore || !back.noDelete || back.debounce != time.Second || back.user != "ubuntu" || back.identity != "/k" || strings.Join(back.excludes, " ") != "data" {
		t.Errorf("round trip: %+v", back)
	}
	args := mountChildArgs("/bin/pmox", back, o.Server, "/src", "web1", "/opt/app", o.Insecure, o.Extra)
	for _, want := range []string{"--server https://pve:8006/api2/json", "--ssh-insecure", "--gitignore", "--no-delete", "--debounce 1s", "--exclude=data", "/src web1:/opt/app -- --bwlimit=100"} {
		if !strings.Contains(strings.Join(args, " "), want) {
			t.Errorf("child args lack %q: %v", want, args)
		}
	}
}

func remountCmd(t *testing.T, args ...string) (*cobra.Command, *remountFlags) {
	t.Helper()
	f := &remountFlags{}
	cmd := newRemountCmd()
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// Re-read the flags into f the way RunE's closure would.
	f.gitignore, _ = cmd.Flags().GetBool("gitignore")
	f.noDelete, _ = cmd.Flags().GetBool("no-delete")
	f.excludes, _ = cmd.Flags().GetStringArray("exclude")
	return cmd, f
}

func TestApplyRemountFlags(t *testing.T) {
	o := mount.Options{Gitignore: true, Excludes: []string{"a"}, NoDelete: true}
	cmd, f := remountCmd(t, "--gitignore=false", "--exclude=b", "--exclude=c")
	applyRemountFlags(cmd, f, &o)
	if o.Gitignore || !o.NoDelete || strings.Join(o.Excludes, " ") != "b c" {
		t.Errorf("options = %+v", o)
	}
}

func TestRemount(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	stateDir, err := mount.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	var started [][]string
	orig := mountStartDaemonFn
	mountStartDaemonFn = func(dir, exe string, args []string, rec mount.Record) (mount.Record, error) {
		started = append(started, args)
		rec.PID = 4242
		return mount.Save(dir, rec)
	}
	t.Cleanup(func() { mountStartDaemonFn = orig })
	// A dead mount with options, one from an older pmox, one with a relative path.
	for _, r := range []mount.Record{
		{VMName: "web1", LocalPath: src, RemotePath: "/opt/app", PID: 999999, Options: &mount.Options{Server: "https://pve", Excludes: []string{"x"}}},
		{VMName: "web2", LocalPath: src, RemotePath: "/old", PID: 999998},
		{VMName: "web3", LocalPath: ".", RemotePath: "/rel", PID: 999997, Options: &mount.Options{}},
	} {
		if _, err := mount.Save(stateDir, r); err != nil {
			t.Fatal(err)
		}
	}
	cmd := newRemountCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--stale", "--exclude=y"})
	cmd.SetContext(context.Background())
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "web2:/old") || !strings.Contains(err.Error(), "web3:/rel") {
		t.Errorf("err = %v\n%s", err, out.String())
	}
	if len(started) != 1 || !strings.Contains(strings.Join(started[0], " "), "--exclude=y "+src+" web1:/opt/app") || strings.Contains(strings.Join(started[0], " "), "--exclude=x") {
		t.Errorf("started %q", started)
	}
	recs, _ := mount.List(stateDir)
	for _, r := range recs {
		if r.VMName == "web1" && (r.PID != 4242 || r.Options == nil || strings.Join(r.Options.Excludes, " ") != "y") {
			t.Errorf("web1 record = %+v", r)
		}
	}
	if !strings.Contains(out.String(), "older pmox") || !strings.Contains(out.String(), "relative path") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A failed sync is retried without a file change, and from the second
// failure on the VM's address is looked up again.
func TestWatchAndSyncRetries(t *testing.T) {
	oldMin, oldRun := mountRetryMin, mountRsyncRunFn
	t.Cleanup(func() { mountRetryMin, mountRsyncRunFn = oldMin, oldRun })
	mountRetryMin = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls, refreshed atomic.Int32
	mountRsyncRunFn = func(_ string, args []string, _ io.Writer) error {
		n := calls.Add(1)
		if n <= 3 {
			return errors.New("ssh: connect: no route to host")
		}
		if args[len(args)-1] != "new" {
			t.Errorf("synced to %v, not the refreshed address", args)
		}
		cancel()
		return nil
	}
	refresh := func() ([]string, error) { refreshed.Add(1); return []string{"rsync", "new"}, nil }
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	dir := t.TempDir()
	var log bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- watchAndSync(cmd, "rsync", []string{"rsync", "old"}, dir, time.Millisecond, &log, refresh)
	}()
	// The first sync comes from a change.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done: //nolint:errcheck // only waiting for the run to finish
	case <-time.After(5 * time.Second):
		t.Fatalf("no recovery; log:\n%s", log.String())
	}
	if refreshed.Load() == 0 || !strings.Contains(log.String(), "back in sync after 3 failed attempt(s)") {
		t.Errorf("refreshed %d; log:\n%s", refreshed.Load(), log.String())
	}
}
