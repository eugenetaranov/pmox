package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/server"
)

func TestYardoSSHArgs(t *testing.T) {
	cmd := bootstrap.YardoInstallCommand("v0.1.0")
	got := yardoSSHArgs(&sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/k/id"}, []string{"-o", "StrictHostKeyChecking=accept-new"}, cmd)
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=accept-new", "-i", "/k/id", "ubuntu@10.0.0.5", cmd}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q\nwant %q", got, want)
	}
	if !strings.Contains(cmd, "sudo -n") || strings.Contains(cmd, "/var/lib/yardo") {
		t.Errorf("install command must use sudo and leave saved answers alone: %s", cmd)
	}
}

func TestApplyYardoPush(t *testing.T) {
	var got *sshTarget
	old := yardoPushFn
	yardoPushFn = func(_ context.Context, target *sshTarget) error { got = target; return nil }
	t.Cleanup(func() { yardoPushFn = old })

	opts := launch.Options{Name: "web1", User: "ubuntu", SSHKeyPath: "/k/id"}
	applyYardoPush(&opts)
	if opts.PushYardoFn != nil {
		t.Fatal("push wired with yardo off")
	}
	opts.Yardo = true
	applyYardoPush(&opts)
	if err := opts.PushYardoFn(context.Background(), "10.0.0.5"); err != nil {
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

func TestApplyYardoProfileOpt(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "team.toml")
	if err := os.WriteFile(good, []byte(`picks = ["cli"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte(`picks = ["cli"`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := &launch.Options{Yardo: true, Name: "web1", User: "pmox"}
	if err := applyYardoProfileOpt(opts, "", &config.Server{}); err != nil || opts.YardoProfileFn != nil {
		t.Errorf("no profile: err %v, fn set %v", err, opts.YardoProfileFn != nil)
	}
	if err := applyYardoProfileOpt(opts, "", &config.Server{YardoProfile: good}); err != nil || opts.YardoProfileFn == nil {
		t.Errorf("config profile: err %v", err)
	}
	if err := applyYardoProfileOpt(&launch.Options{Yardo: true}, bad, nil); err == nil || !strings.Contains(err.Error(), "bad.toml") {
		t.Errorf("bad profile: err = %v", err)
	}
	if err := applyYardoProfileOpt(&launch.Options{}, good, nil); err == nil || !strings.Contains(err.Error(), "yardo: false") {
		t.Errorf("yardo off: err = %v", err)
	}

	var sent []byte
	orig := yardoProfileFn
	t.Cleanup(func() { yardoProfileFn = orig })
	yardoProfileFn = func(_ context.Context, target *sshTarget, prof []byte, _, _ io.Writer) error {
		sent = prof
		if target.IP != "10.0.0.5" || target.User != "pmox" {
			t.Errorf("target = %+v", target)
		}
		return nil
	}
	if err := opts.YardoProfileFn(context.Background(), "10.0.0.5", io.Discard, io.Discard); err != nil || string(sent) != `picks = ["cli"]` {
		t.Errorf("sent %q err %v", sent, err)
	}
	if !strings.Contains(yardoProfileCommand, "yardo --profile "+yardoProfileRemote+" --yes") {
		t.Errorf("command = %s", yardoProfileCommand)
	}
}

// A launch drops the host key a deleted VM pinned for the new VM's IP,
// and only that one.
func TestForgetHostKeyAtLaunch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := guestKnownHostsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	pins := "192.168.0.17 ssh-ed25519 AAAAold\n192.168.0.95 ssh-ed25519 AAAAkeep\n"
	if err := os.WriteFile(path, []byte(pins), 0o600); err != nil {
		t.Fatal(err)
	}
	var opts launch.Options
	applyForgetHostKey(&opts)
	opts.NewIPFn("192.168.0.17")
	b, _ := os.ReadFile(path)
	if string(b) != "192.168.0.95 ssh-ed25519 AAAAkeep\n" {
		t.Errorf("known_hosts_guests = %q", b)
	}
}
