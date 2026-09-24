package credstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testFile returns a file backend rooted in a temp dir, and its path.
func testFile(t *testing.T) (*fileBackend, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pmox", "secrets.yaml")
	return &fileBackend{path: func() (string, error) { return p, nil }}, p
}

// newTestStore builds a store with a fixed mode and keychain availability
// over the given backends. The keychain default is the MockInit-backed
// keyring (see credstore_test.go init).
func newTestStore(m mode, keychainUp bool, kb backend, fb *fileBackend) *store {
	if kb == nil {
		kb = keychainBackend{}
	}
	return &store{
		mode:              func() mode { return m },
		keychainAvailable: func() bool { return keychainUp },
		keychain:          kb,
		file:              fb,
	}
}

// memKeychain is an in-memory keychain stand-in whose operations can be
// forced to fail with a hard (non-NotFound) error.
type memKeychain struct {
	m       map[string]string
	hardErr error
	calls   int
}

func newMemKeychain() *memKeychain { return &memKeychain{m: map[string]string{}} }

func (k *memKeychain) get(account string) (string, error) {
	k.calls++
	if k.hardErr != nil {
		return "", k.hardErr
	}
	v, ok := k.m[account]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	return v, nil
}

func (k *memKeychain) set(account, secret string) error {
	k.calls++
	if k.hardErr != nil {
		return k.hardErr
	}
	k.m[account] = secret
	return nil
}

func (k *memKeychain) remove(account string) error {
	k.calls++
	if k.hardErr != nil {
		return k.hardErr
	}
	if _, ok := k.m[account]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	delete(k.m, account)
	return nil
}

func TestFileBackend_RoundTripAndPerms(t *testing.T) {
	fb, p := testFile(t)
	s := newTestStore(modeFile, false, nil, fb)

	url := "https://pve.lan:8006/api2/json"
	if err := s.set(url, "tok"); err != nil {
		t.Fatalf("Set token: %v", err)
	}
	if err := s.set(url+suffixSSHPassword, "pw"); err != nil {
		t.Fatalf("Set password: %v", err)
	}
	if v, err := s.get(url); err != nil || v != "tok" {
		t.Errorf("Get token = %q, %v", v, err)
	}
	if v, err := s.get(url + suffixSSHPassword); err != nil || v != "pw" {
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
	fb, _ := testFile(t)
	s := newTestStore(modeFile, false, nil, fb)
	if _, err := s.get("https://nope.lan:8006/api2/json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAuto_FallsBackToFileWhenNoKeychain(t *testing.T) {
	fb, _ := testFile(t)
	s := newTestStore(modeAuto, false, nil, fb)

	if got := s.activeBackend(); got != BackendFile {
		t.Errorf("ActiveBackend = %q, want file", got)
	}
	url := "https://pve.lan:8006/api2/json"
	if err := s.set(url, "s"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := s.get(url); err != nil || v != "s" {
		t.Errorf("Get = %q, %v", v, err)
	}
}

func TestAuto_TolerantReadFindsFileSecret(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	fb, _ := testFile(t)
	kc := newMemKeychain()

	// Store only in the file backend.
	if err := newTestStore(modeFile, true, kc, fb).set(url, "from-file"); err != nil {
		t.Fatalf("file Set: %v", err)
	}
	// Auto with a keychain that is available but has no entry.
	if v, err := newTestStore(modeAuto, true, kc, fb).get(url); err != nil || v != "from-file" {
		t.Errorf("tolerant read = %q, %v; want from-file", v, err)
	}
}

func TestAuto_KeychainHardErrorNotMaskedAsNotFound(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	fb, _ := testFile(t)
	kc := newMemKeychain()
	kc.hardErr = errors.New("keychain locked")
	s := newTestStore(modeAuto, true, kc, fb)

	_, err := s.get(url)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("want the keychain error, got %v", err)
	}
	if !errors.Is(err, kc.hardErr) {
		t.Errorf("error does not wrap keychain error: %v", err)
	}

	// A secret that does live in the file is still found.
	if err := fb.set(url, "from-file"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.get(url); err != nil || v != "from-file" {
		t.Errorf("get = %q, %v; want from-file", v, err)
	}
}

func TestKeychainMode_ErrorsWithoutKeychain(t *testing.T) {
	fb, _ := testFile(t)
	s := newTestStore(modeKeychain, false, nil, fb)
	if err := s.set("https://x:8006/api2/json", "s"); err == nil {
		t.Error("Set should error in keychain mode with no keychain")
	}
	if _, err := s.get("https://x:8006/api2/json"); err == nil {
		t.Error("Get should error in keychain mode with no keychain")
	}
}

func TestRemove_ClearsBothBackends(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	fb, _ := testFile(t)
	kc := newMemKeychain()

	// Put the secret in BOTH backends.
	if err := fb.set(url, "in-file"); err != nil {
		t.Fatal(err)
	}
	if err := kc.set(url, "in-keychain"); err != nil {
		t.Fatal(err)
	}

	// Remove under auto → clears both.
	if err := newTestStore(modeAuto, true, kc, fb).remove(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := fb.get(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("file entry not cleared: %v", err)
	}
	if _, err := kc.get(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("keychain entry not cleared: %v", err)
	}
}

func TestRemove_FileModeNeverTouchesKeychain(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	fb, _ := testFile(t)
	kc := newMemKeychain()
	probed := false
	s := newTestStore(modeFile, true, kc, fb)
	s.keychainAvailable = func() bool { probed = true; return true }

	if err := fb.set(url, "in-file"); err != nil {
		t.Fatal(err)
	}
	if err := s.remove(url); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if probed || kc.calls != 0 {
		t.Errorf("file-mode Remove touched the keychain (probed=%v, calls=%d)", probed, kc.calls)
	}
	if err := s.remove(url); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove: want ErrNotFound, got %v", err)
	}
}

func TestRemoveAll(t *testing.T) {
	url := "https://pve.lan:8006/api2/json"
	fb, _ := testFile(t)
	kc := newMemKeychain()
	s := newTestStore(modeAuto, true, kc, fb)

	// Token in the keychain, SSH password in the file, no passphrase.
	if err := kc.set(url, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := fb.set(url+suffixSSHPassword, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.removeAll(url); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	for _, acct := range []string{url, url + suffixSSHPassword, url + suffixSSHKeyPassphrase} {
		if _, err := s.get(acct); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s not cleared: %v", acct, err)
		}
	}
	// Nothing stored → still nil.
	if err := s.removeAll(url); err != nil {
		t.Errorf("RemoveAll on empty: %v", err)
	}
	// Hard keychain error surfaces.
	kc.hardErr = errors.New("denied")
	if err := s.removeAll(url); !errors.Is(err, kc.hardErr) {
		t.Errorf("RemoveAll hard error = %v, want denied", err)
	}
}

func TestActiveBackend_ModeOverrides(t *testing.T) {
	fb, _ := testFile(t)
	if got := newTestStore(modeKeychain, false, nil, fb).activeBackend(); got != BackendKeychain {
		t.Errorf("keychain mode reported %q", got)
	}
	if got := newTestStore(modeFile, true, nil, fb).activeBackend(); got != BackendFile {
		t.Errorf("file mode reported %q", got)
	}
}

func TestModeFromEnv(t *testing.T) {
	cases := map[string]mode{
		"":          modeAuto,
		"auto":      modeAuto,
		"bogus":     modeAuto,
		"keychain":  modeKeychain,
		" FILE ":    modeFile,
		"KeyChain":  modeKeychain,
		"file":      modeFile,
		"something": modeAuto,
	}
	for in, want := range cases {
		t.Setenv("PMOX_SECRET_STORE", in)
		if got := modeFromEnv(); got != want {
			t.Errorf("modeFromEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultSecretsPath_XDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := defaultSecretsPath()
	if err != nil || got != filepath.Join(dir, "pmox", "secrets.yaml") {
		t.Errorf("defaultSecretsPath = %q, %v", got, err)
	}
}
