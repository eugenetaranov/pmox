package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/server"
)

func TestDevboxPushArgs(t *testing.T) {
	got := devboxPushArgs(&sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/k/id"}, []string{"-o", "StrictHostKeyChecking=accept-new"})
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=accept-new", "-i", "/k/id", "ubuntu@10.0.0.5", bootstrap.InstallCommand}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q\nwant %q", got, want)
	}
	if !strings.Contains(bootstrap.InstallCommand, "sudo -n") || strings.Contains(bootstrap.InstallCommand, "/var/lib/devbox-setup") {
		t.Errorf("install command must use sudo and leave saved answers alone: %s", bootstrap.InstallCommand)
	}
}

func TestApplyDevboxPush(t *testing.T) {
	var got *sshTarget
	old := devboxPushFn
	devboxPushFn = func(_ context.Context, target *sshTarget) error { got = target; return nil }
	t.Cleanup(func() { devboxPushFn = old })

	opts := launch.Options{Name: "web1", User: "ubuntu", SSHKeyPath: "/k/id"}
	applyDevboxPush(&opts)
	if opts.PushDevboxFn != nil {
		t.Fatal("push wired with devbox-setup off")
	}
	opts.DevboxSetup = true
	applyDevboxPush(&opts)
	if err := opts.PushDevboxFn(context.Background(), "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != (sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/k/id", Name: "web1"}) {
		t.Errorf("target = %+v", got)
	}
}

func TestResolveVMSpec_ClockSync(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	off := false
	for _, tc := range []struct {
		name string
		srv  config.Server
		f    launchFlags
		want bool
	}{
		{"default on", config.Server{}, launchFlags{}, true},
		{"flag off", config.Server{}, launchFlags{noClockSync: true}, false},
		{"config off", config.Server{ClockSync: &off}, launchFlags{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.srv
			srv.Storage = "local-lvm"
			resolved := &server.Resolved{URL: "https://pve.example:8006/api2/json", Server: &srv, Secret: "s"}
			opts, err := resolveVMSpec(&tc.f, resolved, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if opts.ClockSync != tc.want {
				t.Errorf("ClockSync = %v, want %v", opts.ClockSync, tc.want)
			}
		})
	}
}

func TestLaunchAndCloneHaveNoClockSyncFlag(t *testing.T) {
	if newLaunchCmd().Flags().Lookup("no-clock-sync") == nil || newCloneCmd().Flags().Lookup("no-clock-sync") == nil {
		t.Error("--no-clock-sync missing on launch or clone")
	}
}

func TestApplyDevboxProfileOpt(t *testing.T) {
	old := bootstrap.Bins
	t.Cleanup(func() { bootstrap.Bins = old })
	bootstrap.Bins = fstest.MapFS{"devbox-setup-linux-amd64.gz": {Data: []byte("x")}}

	dir := t.TempDir()
	good := filepath.Join(dir, "team.toml")
	if err := os.WriteFile(good, []byte(`picks = ["cli"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte(`pick = ["cli"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := &launch.Options{DevboxSetup: true, Name: "web1", User: "pmox"}
	if err := applyDevboxProfileOpt(opts, "", &config.Server{}); err != nil || opts.DevboxProfileFn != nil {
		t.Errorf("no profile: err %v, fn set %v", err, opts.DevboxProfileFn != nil)
	}
	if err := applyDevboxProfileOpt(opts, "", &config.Server{DevboxProfile: good}); err != nil || opts.DevboxProfileFn == nil {
		t.Errorf("config profile: err %v", err)
	}
	if err := applyDevboxProfileOpt(&launch.Options{DevboxSetup: true}, bad, nil); err == nil || !strings.Contains(err.Error(), "unknown key pick") {
		t.Errorf("bad profile: err = %v", err)
	}
	if err := applyDevboxProfileOpt(&launch.Options{}, good, nil); err == nil || !strings.Contains(err.Error(), "devbox_setup: false") {
		t.Errorf("devbox off: err = %v", err)
	}
	bootstrap.Bins = fstest.MapFS{}
	if err := applyDevboxProfileOpt(&launch.Options{DevboxSetup: true}, good, nil); err == nil || !strings.Contains(err.Error(), "Go devbox-setup") {
		t.Errorf("no Go builds: err = %v", err)
	}

	var sent []byte
	orig := devboxProfileFn
	t.Cleanup(func() { devboxProfileFn = orig })
	devboxProfileFn = func(_ context.Context, target *sshTarget, prof []byte, _, _ io.Writer) error {
		sent = prof
		if target.IP != "10.0.0.5" || target.User != "pmox" {
			t.Errorf("target = %+v", target)
		}
		return nil
	}
	if err := opts.DevboxProfileFn(context.Background(), "10.0.0.5", io.Discard, io.Discard); err != nil || string(sent) != `picks = ["cli"]` {
		t.Errorf("sent %q err %v", sent, err)
	}
	if !strings.Contains(devboxProfileCommand, "devbox-setup --profile "+devboxProfileRemote+" --yes") {
		t.Errorf("command = %s", devboxProfileCommand)
	}
}
