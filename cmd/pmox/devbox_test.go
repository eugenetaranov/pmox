package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
