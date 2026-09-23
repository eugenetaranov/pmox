package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/tackprofile"
)

func setupTackDir(t *testing.T, files ...string) string {
	t.Helper()
	cfg := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(cfg, "pmox", "tack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("name: x\nhosts: all\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const testURL = "https://pve.lan:8006/api2/json"

func TestResolvePlaybookExplicitWins(t *testing.T) {
	setupTackDir(t, "playbook.yaml")
	explicit := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(explicit, []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pb, record, err := resolvePlaybook(&applyFlags{playbook: explicit}, "web", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != explicit {
		t.Errorf("pb = %q, want %q", pb, explicit)
	}
	if record != "" {
		t.Errorf("record = %q, want empty (explicit must not update memory)", record)
	}
}

func TestResolvePlaybookProfileArg(t *testing.T) {
	dir := setupTackDir(t, "web.yaml")
	pb, record, err := resolvePlaybook(&applyFlags{}, "web", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "web.yaml") {
		t.Errorf("pb = %q", pb)
	}
	if record != "web" {
		t.Errorf("record = %q, want web", record)
	}
}

func TestResolvePlaybookRemembered(t *testing.T) {
	dir := setupTackDir(t, "web.yaml")
	if err := tackprofile.Set(tackStateDir(), testURL, 101, "web"); err != nil {
		t.Fatal(err)
	}
	pb, record, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "web.yaml") {
		t.Errorf("pb = %q, want remembered web.yaml", pb)
	}
	if record != "" {
		t.Errorf("record = %q, want empty (reuse must not re-record)", record)
	}
}

func TestResolvePlaybookDefault(t *testing.T) {
	dir := setupTackDir(t, "playbook.yaml")
	pb, _, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "playbook.yaml") {
		t.Errorf("pb = %q, want default playbook.yaml", pb)
	}
}

func TestResolvePlaybookMissingIsFriendly(t *testing.T) {
	setupTackDir(t) // no files
	_, _, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("want friendly error mentioning --init, got %v", err)
	}
}

func TestApplyInitScaffoldsAndDoesNotClobber(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	cmd := newApplyCmd()

	if err := runApplyInit(cmd); err != nil {
		t.Fatalf("runApplyInit: %v", err)
	}
	pb := filepath.Join(cfg, "pmox", "tack", "playbook.yaml")
	if _, err := os.Stat(pb); err != nil {
		t.Fatalf("playbook not scaffolded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "pmox", "tack", "roles")); err != nil {
		t.Errorf("roles dir not created: %v", err)
	}

	// Second run must not clobber.
	if err := os.WriteFile(pb, []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runApplyInit(cmd); err != nil {
		t.Fatalf("runApplyInit second: %v", err)
	}
	got, _ := os.ReadFile(pb)
	if string(got) != "MINE" {
		t.Errorf("init clobbered existing playbook: %q", got)
	}
}
