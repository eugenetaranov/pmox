package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tackprofile"
)

// testTackStateDir resolves the tack state dir for the test's
// XDG_STATE_HOME.
func testTackStateDir(t *testing.T) string {
	t.Helper()
	dir, err := tackStateDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// With no HOME and no XDG override the tack dirs cannot be resolved; the
// error must propagate instead of silently using a relative path.
func TestTackDirs_PropagateHomeError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := tackDir(); err == nil {
		t.Error("tackDir: want error without HOME")
	}
	if _, err := tackStateDir(); err == nil {
		t.Error("tackStateDir: want error without HOME")
	}
	if _, _, _, err := resolvePlaybook(&applyFlags{}, "web", "u", 1); err == nil {
		t.Error("resolvePlaybook: want error without HOME")
	}
}

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
	pb, record, source, err := resolvePlaybook(&applyFlags{playbook: explicit}, "web", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != explicit {
		t.Errorf("pb = %q, want %q", pb, explicit)
	}
	if record != "" {
		t.Errorf("record = %q, want empty (explicit must not update memory)", record)
	}
	if source != "explicit --playbook" {
		t.Errorf("source = %q, want %q", source, "explicit --playbook")
	}
}

// TestResolvePlaybookExplicitMissingIsFriendly guards a regression: an
// explicit --playbook typo used to skip the existence check entirely
// (only the profile/default branches were stat'd), so pmox would start
// the VM and wait for SSH before tack finally failed on a simple typo.
func TestResolvePlaybookExplicitMissingIsFriendly(t *testing.T) {
	setupTackDir(t, "playbook.yaml")
	missing := filepath.Join(t.TempDir(), "typo.yaml")
	_, _, _, err := resolvePlaybook(&applyFlags{playbook: missing}, "", testURL, 101)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("want a friendly error naming %q, got %v", missing, err)
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("missing explicit playbook should map to ErrUserInput, got %v", err)
	}
}

func TestResolvePlaybookProfileArg(t *testing.T) {
	dir := setupTackDir(t, "web.yaml")
	pb, record, source, err := resolvePlaybook(&applyFlags{}, "web", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "web.yaml") {
		t.Errorf("pb = %q", pb)
	}
	if record != "web" {
		t.Errorf("record = %q, want web", record)
	}
	if source != `profile "web"` {
		t.Errorf("source = %q, want %q", source, `profile "web"`)
	}
}

func TestResolvePlaybookRemembered(t *testing.T) {
	dir := setupTackDir(t, "web.yaml")
	if err := tackprofile.Set(testTackStateDir(t), testURL, 101, "web"); err != nil {
		t.Fatal(err)
	}
	pb, record, source, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "web.yaml") {
		t.Errorf("pb = %q, want remembered web.yaml", pb)
	}
	if record != "" {
		t.Errorf("record = %q, want empty (reuse must not re-record)", record)
	}
	if source != `remembered profile "web"` {
		t.Errorf("source = %q, want %q", source, `remembered profile "web"`)
	}
}

func TestResolvePlaybookDefault(t *testing.T) {
	dir := setupTackDir(t, "playbook.yaml")
	pb, _, source, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err != nil {
		t.Fatalf("resolvePlaybook: %v", err)
	}
	if pb != filepath.Join(dir, "playbook.yaml") {
		t.Errorf("pb = %q, want default playbook.yaml", pb)
	}
	if source != "default playbook" {
		t.Errorf("source = %q, want %q", source, "default playbook")
	}
}

func TestResolvePlaybookMissingIsFriendly(t *testing.T) {
	setupTackDir(t) // no files
	_, _, _, err := resolvePlaybook(&applyFlags{}, "", testURL, 101)
	if err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("want friendly error mentioning --init, got %v", err)
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("missing playbook should map to ErrUserInput, got %v", err)
	}
}

func TestApplyNoConfigSuggestsInitEarly(t *testing.T) {
	// No tack dir at all: apply must guide to --init before touching the
	// cluster/picker (so this needs no configured server).
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	cmd := newApplyCmd()
	cmd.SetContext(context.Background())
	err := runApply(cmd, nil, &applyFlags{})
	if err == nil || !strings.Contains(err.Error(), "--init") {
		t.Fatalf("want early --init hint, got %v", err)
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("no-config apply should map to ErrUserInput, got %v", err)
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

// TestStarterPlaybookRoleHasRolesPrefix guards a regression: the
// scaffolded starter playbook referenced the docker role as
// "tack-roles.git//docker", but tack-roles.git actually keeps every
// role under a roles/ directory ("roles/docker"). The wrong path made
// the very first `pmox apply <vm>` after `--init` fail during role
// resolution, on a file pmox itself generated. Confirmed against a
// live clone of tackhq/tack-roles and a real `tack run --check`.
func TestStarterPlaybookRoleHasRolesPrefix(t *testing.T) {
	if !strings.Contains(starterPlaybook, "tack-roles.git//roles/docker") {
		t.Errorf("starterPlaybook role path missing the roles/ prefix:\n%s", starterPlaybook)
	}
}

// TestStarterPlaybookHasSudo guards a second regression alongside the
// roles/ prefix fix: the docker role installs packages and manages a
// systemd service, both of which need root (its own README says so),
// but the scaffolded playbook never declared sudo: true. The very
// first `pmox apply <vm>` after `--init` applied cleanly through
// planning, then failed on the actual apt-get with a permission error
// — again on a file pmox itself generated. Confirmed against a live
// VM: sudo: true (inherited by every task in the play) fixes it.
func TestStarterPlaybookHasSudo(t *testing.T) {
	if !strings.Contains(starterPlaybook, "sudo: true") {
		t.Errorf("starterPlaybook missing 'sudo: true':\n%s", starterPlaybook)
	}
}

// TestTackRunError_ExitsAsHook guards the exit-code consistency fix: a
// failed tack run through `pmox apply` now maps to the same ExitHook
// code a failed `pmox launch --tack` hook uses, instead of collapsing
// to the generic exit 1 every other apply error also uses.
func TestTackRunError_ExitsAsHook(t *testing.T) {
	err := &tackRunError{err: errors.New("exit status 1")}
	if got := exitcode.From(err); got != exitcode.ExitHook {
		t.Errorf("exitcode.From(tackRunError) = %d, want ExitHook (%d)", got, exitcode.ExitHook)
	}
	if !strings.Contains(err.Error(), "tack run failed") {
		t.Errorf("Error() = %q, want it to mention 'tack run failed'", err.Error())
	}
}
