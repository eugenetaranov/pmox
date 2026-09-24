package tackprofile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetGetRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tack")
	const url = "https://pve.lan:8006/api2/json"

	if _, ok, _ := Get(dir, url, 101); ok {
		t.Fatal("expected no profile before Set")
	}
	if err := Set(dir, url, 101, "web"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := Get(dir, url, 101)
	if err != nil || !ok || got != "web" {
		t.Fatalf("Get = %q ok=%v err=%v, want web", got, ok, err)
	}

	// State file is 0600.
	if fi, _ := os.Stat(filepath.Join(dir, "profiles.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestKeyedByServerAndVMID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tack")
	_ = Set(dir, "https://a:8006/api2/json", 101, "web")
	_ = Set(dir, "https://b:8006/api2/json", 101, "db")
	_ = Set(dir, "https://a:8006/api2/json", 102, "cache")

	if v, _, _ := Get(dir, "https://a:8006/api2/json", 101); v != "web" {
		t.Errorf("a/101 = %q, want web", v)
	}
	if v, _, _ := Get(dir, "https://b:8006/api2/json", 101); v != "db" {
		t.Errorf("b/101 = %q, want db", v)
	}
	if v, _, _ := Get(dir, "https://a:8006/api2/json", 102); v != "cache" {
		t.Errorf("a/102 = %q, want cache", v)
	}
}

func TestCorruptStateIsIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tack")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Get(dir, "https://a:8006/api2/json", 1); ok || err != nil {
		t.Errorf("corrupt state: ok=%v err=%v, want false/nil", ok, err)
	}
	// The corrupt file is preserved aside rather than overwritten later.
	if got, err := os.ReadFile(filepath.Join(dir, "profiles.json.corrupt")); err != nil || string(got) != "{not json" {
		t.Errorf("corrupt file not moved aside: %q, %v", got, err)
	}
	// And Set still works (starts a fresh state file).
	if err := Set(dir, "https://a:8006/api2/json", 1, "x"); err != nil {
		t.Fatalf("Set after corrupt: %v", err)
	}
	if v, ok, _ := Get(dir, "https://a:8006/api2/json", 1); !ok || v != "x" {
		t.Errorf("Get after Set = %q, %v", v, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.json.corrupt")); err != nil {
		t.Errorf("corrupt backup lost after Set: %v", err)
	}
}
