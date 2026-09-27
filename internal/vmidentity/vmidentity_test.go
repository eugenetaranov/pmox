package vmidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetGetRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vmidentity")
	const url = "https://pve.lan:8006/api2/json"

	if _, ok, _ := Get(dir, url, 101); ok {
		t.Fatal("expected no identity before Set")
	}
	want := Identity{User: "ubuntu", SSHPubkeyLine: "ssh-ed25519 AAAA test@host"}
	if err := Set(dir, url, 101, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := Get(dir, url, 101)
	if err != nil || !ok || got != want {
		t.Fatalf("Get = %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}

	// State file is 0600.
	if fi, _ := os.Stat(filepath.Join(dir, "identities.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestKeyedByServerAndVMID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vmidentity")
	_ = Set(dir, "https://a:8006/api2/json", 101, Identity{User: "ubuntu"})
	_ = Set(dir, "https://b:8006/api2/json", 101, Identity{User: "root"})
	_ = Set(dir, "https://a:8006/api2/json", 102, Identity{User: "e"})

	if v, _, _ := Get(dir, "https://a:8006/api2/json", 101); v.User != "ubuntu" {
		t.Errorf("a/101 = %+v, want user ubuntu", v)
	}
	if v, _, _ := Get(dir, "https://b:8006/api2/json", 101); v.User != "root" {
		t.Errorf("b/101 = %+v, want user root", v)
	}
	if v, _, _ := Get(dir, "https://a:8006/api2/json", 102); v.User != "e" {
		t.Errorf("a/102 = %+v, want user e", v)
	}
}

func TestCorruptStateIsIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vmidentity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identities.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Get(dir, "https://a:8006/api2/json", 1); ok || err != nil {
		t.Errorf("corrupt state: ok=%v err=%v, want false/nil", ok, err)
	}
	// The corrupt file is preserved aside rather than overwritten later.
	if got, err := os.ReadFile(filepath.Join(dir, "identities.json.corrupt")); err != nil || string(got) != "{not json" {
		t.Errorf("corrupt file not moved aside: %q, %v", got, err)
	}
	// And Set still works (starts a fresh state file).
	if err := Set(dir, "https://a:8006/api2/json", 1, Identity{User: "x"}); err != nil {
		t.Fatalf("Set after corrupt: %v", err)
	}
	if v, ok, _ := Get(dir, "https://a:8006/api2/json", 1); !ok || v.User != "x" {
		t.Errorf("Get after Set = %+v, %v", v, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "identities.json.corrupt")); err != nil {
		t.Errorf("corrupt backup lost after Set: %v", err)
	}
}
