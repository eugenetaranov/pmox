package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/mount"
)

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

func TestKnownHostToken(t *testing.T) {
	cases := map[string]string{
		"192.168.0.60 ssh-ed25519 AAAA":     "192.168.0.60",
		"[192.168.0.60]:22 ssh-rsa BBBB":    "192.168.0.60",
		"host.lan,10.0.0.1 ssh-ed25519 C":   "host.lan",
		"":                                  "",
	}
	for in, want := range cases {
		if got := knownHostToken(in); got != want {
			t.Errorf("knownHostToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalMountItems_DeadRecordAndOrphanLog(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := mountStateDir()
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
