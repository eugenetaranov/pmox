package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runAutostart(t *testing.T, goos, arg string) []string {
	t.Helper()
	var ran []string
	oldRun, oldOS := autostartRunFn, autostartGOOS
	t.Cleanup(func() { autostartRunFn, autostartGOOS = oldRun, oldOS })
	autostartRunFn = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	autostartGOOS = goos
	cmd := newMountAutostartCmd()
	cmd.SetArgs([]string{arg})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return ran
}

func TestAutostartMacOS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ran := runAutostart(t, "darwin", "on")
	p := filepath.Join(home, "Library", "LaunchAgents", autostartLabel+".plist")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>remount</string>", "<string>--stale</string>", "<key>RunAtLoad</key>", "<integer>300</integer>", "<key>AbandonProcessGroup</key>"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	if len(ran) != 2 || !strings.HasPrefix(ran[1], "launchctl bootstrap gui/") || !strings.HasSuffix(ran[1], p) {
		t.Errorf("ran %q", ran)
	}
	runAutostart(t, "darwin", "off")
	if _, err := os.Stat(p); err == nil {
		t.Error("plist left after off")
	}
}

func TestAutostartLinux(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	ran := runAutostart(t, "linux", "on")
	b, err := os.ReadFile(filepath.Join(cfg, "systemd", "user", "pmox-mounts.service"))
	if err != nil || !strings.Contains(string(b), " remount --stale\n") || !strings.Contains(string(b), "KillMode=process") {
		t.Errorf("service (%v):\n%s", err, b)
	}
	if strings.Join(ran, "; ") != "systemctl --user daemon-reload; systemctl --user enable --now pmox-mounts.timer" {
		t.Errorf("ran %q", ran)
	}
	runAutostart(t, "linux", "off")
	if _, err := os.Stat(filepath.Join(cfg, "systemd", "user", "pmox-mounts.timer")); err == nil {
		t.Error("timer left after off")
	}
}
