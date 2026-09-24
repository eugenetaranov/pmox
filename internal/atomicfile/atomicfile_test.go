package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_CreatesDirAndFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f.yaml")
	if err := Write(p, []byte("hello"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "hello" {
		t.Fatalf("read back = %q, %v", got, err)
	}
	fi, _ := os.Stat(p)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
	di, _ := os.Stat(filepath.Dir(p))
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 0700", perm)
	}
}

func TestWrite_ReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
	fi, _ := os.Stat(p)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (temp file left behind?)", len(entries))
	}
}
