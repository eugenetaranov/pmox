// Package credstore persists pmox secrets (API token, node SSH password,
// key passphrase) keyed by canonical server URL. Secrets are stored in
// the OS keychain when one is available, otherwise in a permission-
// restricted file (see fileBackend); the choice is automatic unless
// PMOX_SECRET_STORE forces a backend. The package-level Get/Set/Remove
// and the node-SSH helpers are the stable API used across pmox.
package credstore

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const service = "pmox"

// Suffixes appended to a canonical server URL to form the account for
// secrets other than the main API token. The bare URL holds the token.
const (
	suffixSSHPassword      = "#node_ssh_password"
	suffixSSHKeyPassphrase = "#node_ssh_key_passphrase"
)

// ErrNotFound is returned when no secret exists for an account.
var ErrNotFound = errors.New("secret not found")

// GetNodeSSHPassword returns the SSH password for url, or ErrNotFound.
func GetNodeSSHPassword(url string) (string, error) { return Get(url + suffixSSHPassword) }

// SetNodeSSHPassword stores the SSH password for url.
func SetNodeSSHPassword(url, value string) error { return Set(url+suffixSSHPassword, value) }

// RemoveNodeSSHPassword deletes the SSH password entry for url.
func RemoveNodeSSHPassword(url string) error { return Remove(url + suffixSSHPassword) }

// GetNodeSSHKeyPassphrase returns the SSH key passphrase for url.
func GetNodeSSHKeyPassphrase(url string) (string, error) {
	return Get(url + suffixSSHKeyPassphrase)
}

// SetNodeSSHKeyPassphrase stores the SSH key passphrase for url.
func SetNodeSSHKeyPassphrase(url, value string) error {
	return Set(url+suffixSSHKeyPassphrase, value)
}

// RemoveNodeSSHKeyPassphrase deletes the SSH key passphrase entry for url.
func RemoveNodeSSHKeyPassphrase(url string) error {
	return Remove(url + suffixSSHKeyPassphrase)
}

// RemoveAll deletes every secret pmox keeps for url: the API token, the
// node SSH password and the node SSH key passphrase. Entries that do not
// exist are ignored; it returns nil when nothing was stored. Hard errors
// from any removal are joined so one failure doesn't skip the others.
func RemoveAll(url string) error { return defaultStore.removeAll(url) }

// Backend names the secret store a secret resolves to.
type Backend string

// Backends reported by ActiveBackend.
const (
	BackendKeychain Backend = "keychain"
	BackendFile     Backend = "file"
)

// mode is the PMOX_SECRET_STORE selection.
type mode string

const (
	modeAuto     mode = "auto"
	modeKeychain mode = "keychain"
	modeFile     mode = "file"
)

// backend is one secret store keyed by an opaque account string
// (a URL, or a URL plus a suffix). get/remove return ErrNotFound when
// the account is absent.
type backend interface {
	get(account string) (string, error)
	set(account, secret string) error
	remove(account string) error
}

// store routes secret operations to the keychain and/or file backend
// according to the selected mode.
type store struct {
	// mode resolves the backend selection. The default store reads
	// PMOX_SECRET_STORE on every call, so the environment stays the
	// single source of truth (and tests may change it with t.Setenv).
	mode func() mode
	// keychainAvailable reports whether the OS keychain is usable.
	keychainAvailable func() bool
	keychain          backend
	file              *fileBackend
}

// defaultStore backs the package-level API. Its keychain probe runs at
// most once per process; the secrets file path is resolved per call
// from the XDG environment.
var defaultStore = &store{
	mode:              modeFromEnv,
	keychainAvailable: cachedKeychainProbe(),
	keychain:          keychainBackend{},
	file:              &fileBackend{path: defaultSecretsPath},
}

// modeFromEnv reads PMOX_SECRET_STORE: "keychain", "file", or "auto"
// (default). Unknown values fall back to auto.
func modeFromEnv() mode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PMOX_SECRET_STORE"))) {
	case "keychain":
		return modeKeychain
	case "file":
		return modeFile
	default:
		return modeAuto
	}
}

func cachedKeychainProbe() func() bool {
	var once sync.Once
	var ok bool
	return func() bool {
		once.Do(func() { ok = probeKeychain() })
		return ok
	}
}

// probeKeychain does a sentinel set->get->delete; any failure means the
// keychain is unusable (headless Linux, CI, no Secret Service).
func probeKeychain() bool {
	const probe = "__pmox_keychain_probe__"
	if err := keyring.Set(service, probe, "1"); err != nil {
		return false
	}
	_, getErr := keyring.Get(service, probe)
	_ = keyring.Delete(service, probe)
	return getErr == nil
}

// ActiveBackend returns the backend secrets currently resolve to,
// BackendKeychain or BackendFile — used by `pmox doctor`.
func ActiveBackend() Backend { return defaultStore.activeBackend() }

// Get retrieves the secret for account. Under auto it tries the keychain
// then the file, so a secret stored under either backend is found. A
// keychain failure other than "not found" (locked, access denied) is
// returned when the file has no entry either, rather than being masked
// as ErrNotFound.
func Get(account string) (string, error) { return defaultStore.get(account) }

// Set stores secret under account. Under auto it writes to the keychain
// when available, otherwise the file.
func Set(account, secret string) error { return defaultStore.set(account, secret) }

// Remove deletes account from every backend the mode allows (auto and
// keychain: keychain and file; file: file only, never touching the
// keychain) so no stray secret remains. A missing entry in a backend is
// tolerated; ErrNotFound is returned only when no backend had it.
func Remove(account string) error { return defaultStore.remove(account) }

func (s *store) activeBackend() Backend {
	switch s.mode() {
	case modeKeychain:
		return BackendKeychain
	case modeFile:
		return BackendFile
	default:
		if s.keychainAvailable() {
			return BackendKeychain
		}
		return BackendFile
	}
}

func (s *store) get(account string) (string, error) {
	switch s.mode() {
	case modeKeychain:
		if !s.keychainAvailable() {
			return "", errKeychainForced()
		}
		return s.keychain.get(account)
	case modeFile:
		return s.file.get(account)
	default:
		if !s.keychainAvailable() {
			return s.file.get(account)
		}
		v, kerr := s.keychain.get(account)
		if kerr == nil {
			return v, nil
		}
		v, ferr := s.file.get(account)
		switch {
		case ferr == nil:
			return v, nil
		case errors.Is(kerr, ErrNotFound):
			return "", ferr
		case errors.Is(ferr, ErrNotFound):
			return "", kerr
		default:
			return "", errors.Join(kerr, ferr)
		}
	}
}

func (s *store) set(account, secret string) error {
	switch s.mode() {
	case modeKeychain:
		if !s.keychainAvailable() {
			return errKeychainForced()
		}
		return s.keychain.set(account, secret)
	case modeFile:
		return s.file.set(account, secret)
	default:
		if s.keychainAvailable() {
			return s.keychain.set(account, secret)
		}
		return s.file.set(account, secret)
	}
}

func (s *store) remove(account string) error {
	found := false
	var hardErr error
	record := func(err error) {
		switch {
		case err == nil:
			found = true
		case errors.Is(err, ErrNotFound):
			// tolerated
		default:
			if hardErr == nil {
				hardErr = err
			}
		}
	}
	if s.mode() != modeFile && s.keychainAvailable() {
		record(s.keychain.remove(account))
	}
	record(s.file.remove(account))
	if hardErr != nil {
		return hardErr
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	return nil
}

func (s *store) removeAll(url string) error {
	var errs []error
	for _, account := range []string{url, url + suffixSSHPassword, url + suffixSSHKeyPassphrase} {
		if err := s.remove(account); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func errKeychainForced() error {
	return fmt.Errorf("PMOX_SECRET_STORE=keychain but the OS keychain is unavailable; unset it to fall back to the secrets file, or set PMOX_SECRET_STORE=file")
}

// --- keychain backend ---

type keychainBackend struct{}

func (keychainBackend) get(account string) (string, error) {
	secret, err := keyring.Get(service, account)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, account)
		}
		return "", wrapKeychainErr(err)
	}
	return secret, nil
}

func (keychainBackend) set(account, secret string) error {
	if err := keyring.Set(service, account, secret); err != nil {
		return wrapKeychainErr(err)
	}
	return nil
}

func (keychainBackend) remove(account string) error {
	if err := keyring.Delete(service, account); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, account)
		}
		return wrapKeychainErr(err)
	}
	return nil
}

func wrapKeychainErr(err error) error {
	if runtime.GOOS == "linux" {
		return fmt.Errorf("system keychain error: %w; install gnome-keyring or KWallet, or set PMOX_SECRET_STORE=file", err)
	}
	return fmt.Errorf("keychain error: %w", err)
}
