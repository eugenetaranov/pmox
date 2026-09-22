package credstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// useFileStore points secretsPath at a temp file and restores it after.
func useFileStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pmox", "secrets.yaml")
	orig := secretsPath
	secretsPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { secretsPath = orig })
	return p
}

// setKeychain overrides the availability probe for a test.
func setKeychain(t *testing.T, available bool) {
	t.Helper()
	orig := keychainAvailable
	keychainAvailable = func() bool { return available }
	t.Cleanup(func() { keychainAvailable = orig })
}

func TestFileBackend_RoundTripAndPerms(t *testing.T) {
	t.Setenv("PMOX_SECRET_STORE", "file")
	p := useFileStore(t)

	url := "https://pve.lan:8006/api2/json"
	if err := Set(url, "tok"); err != nil {
		t.Fatalf("Set token: %v", err)
	}
	if err := SetNodeSSHPassword(url, "pw"); err != nil {
		t.Fatalf("Set password: %v", err)
	}
	if v, err := Get(url); err != nil || v != "tok" {
		t.Errorf("Get token = %q, %v", v, err)
	}
	if v, err := GetNodeSSHPassword(url); err != nil || v != "pw" {
		t.Errorf("Get password = %q, %v", v, err)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat secrets: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("secrets.yaml mode = %o, want 0600", perm)
	}
	di, _ := os.Stat(filepath.Dir(p))
	if perm := di.Mode().Perm(); perm != fs.FileMode(0o700) {
		t.Errorf("secrets dir mode = %o, want 0700", perm)
	}
}

func TestFileBackend_MissingIsNotFound(t *testing.T) {
	t.Setenv("PMOX_SECRET_STORE", "file")
	useFileStore(t)
	if _, err := Get("https://nope.lan:8006/api2/json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAuto_FallsBackToFileWhenNoKeychain(t *testing.T) {
	t.Setenv("PMOX_SECRET_STORE", "auto")
	useFileStore(t)
	setKeychain(t, false)

	if ActiveBackend() != "file" {
		t.Errorf("ActiveBackend = %q, want file", ActiveBackend())
	}
	url := "https://pve.lan:8006/api2/json"
	if err := Set(url, "s"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := Get(url); err != nil || v != "s" {
		t.Errorf("Get = %q, %v", v, err)
	}
}

func TestAuto_TolerantReadFindsFileSecret(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	useFileStore(t)

	// Store only in the file backend.
	t.Setenv("PMOX_SECRET_STORE", "file")
	if err := Set(url, "from-file"); err != nil {
		t.Fatalf("file Set: %v", err)
	}

	// Now auto with a keychain that is available but has no entry.
	t.Setenv("PMOX_SECRET_STORE", "auto")
	setKeychain(t, true) // keychainBackend is MockInit-backed and empty for this url
	if v, err := Get(url); err != nil || v != "from-file" {
		t.Errorf("tolerant read = %q, %v; want from-file", v, err)
	}
}

func TestKeychainMode_ErrorsWithoutKeychain(t *testing.T) {
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	setKeychain(t, false)
	if err := Set("https://x:8006/api2/json", "s"); err == nil {
		t.Error("Set should error in keychain mode with no keychain")
	}
	if _, err := Get("https://x:8006/api2/json"); err == nil {
		t.Error("Get should error in keychain mode with no keychain")
	}
}

func TestRemove_ClearsBothBackends(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	useFileStore(t)
	setKeychain(t, true) // MockInit keychain is active in this test binary

	// Put the secret in BOTH backends.
	t.Setenv("PMOX_SECRET_STORE", "file")
	if err := Set(url, "in-file"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	if err := Set(url, "in-keychain"); err != nil {
		t.Fatal(err)
	}

	// Remove under auto → clears both.
	t.Setenv("PMOX_SECRET_STORE", "auto")
	if err := Remove(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	t.Setenv("PMOX_SECRET_STORE", "file")
	if _, err := Get(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("file entry not cleared: %v", err)
	}
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	if _, err := Get(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("keychain entry not cleared: %v", err)
	}
}

func TestActiveBackend_ModeOverrides(t *testing.T) {
	setKeychain(t, false)
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	if ActiveBackend() != "keychain" {
		t.Error("keychain mode should report keychain")
	}
	t.Setenv("PMOX_SECRET_STORE", "file")
	if ActiveBackend() != "file" {
		t.Error("file mode should report file")
	}
}
