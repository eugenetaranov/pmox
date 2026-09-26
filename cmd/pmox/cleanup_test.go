package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// stubCleanupConfirmer forces confirmCleanup's answer for a test,
// bypassing the real TTY/stdin read.
func stubCleanupConfirmer(t *testing.T, answer bool) *string {
	t.Helper()
	orig := cleanupConfirmerFn
	var gotPrompt string
	cleanupConfirmerFn = func(*cobra.Command) tui.Confirmer {
		return fixedConfirmer{answer: answer, prompt: &gotPrompt}
	}
	t.Cleanup(func() { cleanupConfirmerFn = orig })
	return &gotPrompt
}

type fixedConfirmer struct {
	answer bool
	prompt *string
}

func (f fixedConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	*f.prompt = prompt
	return f.answer, nil
}

func TestSnippetFileRe(t *testing.T) {
	cases := map[string]string{
		"local:snippets/pmox-104-user-data.yaml": "104",
		"pmox-9-user-data.yaml":                  "9",
		"local:snippets/other.yaml":              "",
		"pmox-abc-user-data.yaml":                "",
	}
	for in, want := range cases {
		m := snippetFileRe.FindStringSubmatch(in)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("snippetFileRe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnippetStoragesFor(t *testing.T) {
	got := snippetStoragesFor(&config.Server{Storage: "vm-data", SnippetStorage: "local"})
	if len(got) != 2 || got[0] != "local" || got[1] != "vm-data" {
		t.Errorf("got %v, want [local vm-data]", got)
	}
	// Dedup when snippet == disk storage.
	got = snippetStoragesFor(&config.Server{Storage: "local", SnippetStorage: "local"})
	if len(got) != 1 || got[0] != "local" {
		t.Errorf("got %v, want [local]", got)
	}
}

func TestLocalMountItems_DeadRecordAndOrphanLog(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := testMountStateDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A record with a dead PID + its log.
	deadPID := 2147480000
	logPath := filepath.Join(dir, "web1-dead.log")
	if err := os.WriteFile(logPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "/src", RemotePath: "/opt", PID: deadPID, LogPath: logPath}); err != nil {
		t.Fatal(err)
	}
	// An orphan log with no record.
	orphan := filepath.Join(dir, "gone-orphan.log")
	if err := os.WriteFile(orphan, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}

	items := localMountItems()
	var recItems, logItems int
	for _, it := range items {
		switch it.Category {
		case "mount-record":
			recItems++
		case "log":
			logItems++
		}
	}
	if recItems != 1 {
		t.Errorf("mount-record items = %d, want 1", recItems)
	}
	if logItems != 1 {
		t.Errorf("orphan-log items = %d, want 1 (%v)", logItems, items)
	}

	// Apply and confirm the dead record's sidecar, its log, and the orphan
	// log are all gone.
	for _, it := range items {
		if err := it.apply(); err != nil {
			t.Errorf("apply %q: %v", it.Detail, err)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphan log should have been removed")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Error("dead record's log should have been removed")
	}
	recs, _ := mount.List(dir)
	if len(recs) != 0 {
		t.Errorf("dead record should have been removed, %d remain", len(recs))
	}
}

func TestStaleKnownHostItems(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := guestKnownHostsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "192.168.0.60 ssh-ed25519 AAAAstale\n192.168.0.99 ssh-ed25519 BBBBlive\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{"192.168.0.99": true}

	// Incomplete IP enumeration → never prunes.
	if items := staleKnownHostItems(live, false, io.Discard); items != nil {
		t.Error("must not prune when the live-IP set is incomplete")
	}

	items := staleKnownHostItems(live, true, io.Discard)
	if len(items) != 1 || !strings.Contains(items[0].Detail, "192.168.0.60") {
		t.Fatalf("expected one stale-pin item naming 192.168.0.60, got %v", items)
	}
	if err := items[0].apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, _ := os.ReadFile(path)
	if strings.Contains(string(after), "192.168.0.60") {
		t.Error("stale pin should have been pruned")
	}
	if !strings.Contains(string(after), "192.168.0.99") {
		t.Error("live pin must be kept")
	}
}

func TestReportCleanup_JSONApplyReportsFailures(t *testing.T) {
	orig := outputMode
	outputMode = "json"
	t.Cleanup(func() { outputMode = orig })

	items := []cleanupItem{
		{Category: "log", Detail: "ok", apply: func() error { return nil }},
		{Category: "log", Detail: "bad", apply: func() error { return errors.New("permission denied") }},
	}
	cmd := newCleanupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := reportCleanup(cmd, items, true, false)
	if err == nil || !strings.Contains(err.Error(), "removed 1 of 2 item(s); 1 failed") {
		t.Fatalf("err = %v, want partial-removal error", err)
	}
	if exitcode.From(err) == exitcode.ExitOK {
		t.Error("partial failure must exit non-zero")
	}
	var got struct {
		Applied bool `json:"applied"`
		Total   int  `json:"total"`
		Failed  int  `json:"failed"`
		Items   []struct {
			Detail string `json:"detail"`
			Error  string `json:"error"`
		} `json:"items"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil {
		t.Fatalf("output is not JSON: %v\n%s", jerr, out.String())
	}
	if !got.Applied || got.Total != 2 || got.Failed != 1 {
		t.Errorf("applied/total/failed = %v/%d/%d, want true/2/1", got.Applied, got.Total, got.Failed)
	}
	if got.Items[0].Error != "" || got.Items[1].Error != "permission denied" {
		t.Errorf("per-item errors = %q, %q", got.Items[0].Error, got.Items[1].Error)
	}
}

func TestReportCleanup_JSONApplySuccess(t *testing.T) {
	orig := outputMode
	outputMode = "json"
	t.Cleanup(func() { outputMode = orig })

	items := []cleanupItem{{Category: "log", Detail: "ok", apply: func() error { return nil }}}
	cmd := newCleanupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := reportCleanup(cmd, items, true, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out.String(), `"error"`) {
		t.Errorf("successful item should omit error: %s", out.String())
	}
}

// --- interactive "Remove N item(s) now?" prompt (dry-run + a TTY) ---

func TestReportCleanup_NonInteractiveDryRunStillNeedsApplyFlag(t *testing.T) {
	// Unaffected by this feature: no terminal (interactive=false) keeps
	// the exact old dry-run behavior, so existing scripts relying on
	// "nothing removed without --apply" see no change at all.
	var ran bool
	items := []cleanupItem{{Category: "log", Detail: "x", apply: func() error { ran = true; return nil }}}
	cmd := newCleanupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := reportCleanup(cmd, items, false, false); err != nil {
		t.Fatalf("reportCleanup: %v", err)
	}
	if ran {
		t.Error("must not remove anything non-interactively without --apply")
	}
	if !strings.Contains(out.String(), "Re-run with --apply") {
		t.Errorf("out = %q, want the old dry-run message", out.String())
	}
}

func TestReportCleanup_InteractiveApprovedRemovesNow(t *testing.T) {
	gotPrompt := stubCleanupConfirmer(t, true)
	var ran bool
	items := []cleanupItem{{Category: "log", Detail: "x", apply: func() error { ran = true; return nil }}}
	cmd := newCleanupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := reportCleanup(cmd, items, false, true); err != nil {
		t.Fatalf("reportCleanup: %v", err)
	}
	if !ran {
		t.Error("saying yes should remove the item in the same run — no re-run with --apply needed")
	}
	if !strings.Contains(out.String(), "Removed 1 item(s)") {
		t.Errorf("out = %q, want the removal summary", out.String())
	}
	if *gotPrompt != "\nRemove 1 item(s) now? [y/N]: " {
		t.Errorf("prompt = %q", *gotPrompt)
	}
}

func TestReportCleanup_InteractiveDeclinedRemovesNothing(t *testing.T) {
	stubCleanupConfirmer(t, false)
	var ran bool
	items := []cleanupItem{{Category: "log", Detail: "x", apply: func() error { ran = true; return nil }}}
	cmd := newCleanupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := reportCleanup(cmd, items, false, true); err != nil {
		t.Fatalf("reportCleanup: %v", err)
	}
	if ran {
		t.Error("saying no must not remove anything")
	}
	if !strings.Contains(out.String(), "Nothing removed") {
		t.Errorf("out = %q, want a plain 'nothing removed' note", out.String())
	}
}

func TestReportCleanup_ExplicitApplySkipsThePrompt(t *testing.T) {
	// --apply is the "just do it, don't ask" escape hatch (for
	// scripts/CI); passing it explicitly must never trigger the
	// interactive confirmation even when a terminal is present.
	cmd := newCleanupCmd()
	orig := cleanupConfirmerFn
	cleanupConfirmerFn = func(*cobra.Command) tui.Confirmer {
		t.Fatal("must not ask for confirmation when --apply was passed explicitly")
		return nil
	}
	t.Cleanup(func() { cleanupConfirmerFn = orig })
	var ran bool
	items := []cleanupItem{{Category: "log", Detail: "x", apply: func() error { ran = true; return nil }}}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := reportCleanup(cmd, items, true, true); err != nil {
		t.Fatalf("reportCleanup: %v", err)
	}
	if !ran {
		t.Error("--apply should remove the item without asking")
	}
}

func TestConfirmCleanup_MentionsDestructiveTemplateDeletion(t *testing.T) {
	gotPrompt := stubCleanupConfirmer(t, false)
	cmd := newCleanupCmd()

	if _, err := confirmCleanup(cmd, []cleanupItem{{Category: "log"}}); err != nil {
		t.Fatalf("confirmCleanup: %v", err)
	}
	if strings.Contains(*gotPrompt, "destructive") {
		t.Errorf("prompt = %q, must not mention destructive deletion when there is none", *gotPrompt)
	}

	if _, err := confirmCleanup(cmd, []cleanupItem{{Category: "template"}}); err != nil {
		t.Fatalf("confirmCleanup: %v", err)
	}
	if !strings.Contains(*gotPrompt, "destructive template deletion") {
		t.Errorf("prompt = %q, want it to call out destructive template deletion", *gotPrompt)
	}
}
