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

// backend is one secret store keyed by an opaque account string
// (a URL, or a URL plus a suffix). get/remove return ErrNotFound when
// the account is absent.
type backend interface {
	get(account string) (string, error)
	set(account, secret string) error
	remove(account string) error
}

var (
	kb backend = keychainBackend{}
	fb backend = fileBackend{}
)

// storeMode reads PMOX_SECRET_STORE: "keychain", "file", or "auto"
// (default). Unknown values fall back to auto.
func storeMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PMOX_SECRET_STORE"))) {
	case "keychain":
		return "keychain"
	case "file":
		return "file"
	default:
		return "auto"
	}
}

// keychainAvailable reports whether the OS keychain is usable. It is a var
// so tests can inject a result; the default probes once per process.
var keychainAvailable = cachedKeychainProbe()

func cachedKeychainProbe() func() bool {
	var once sync.Once
	var ok bool
	return func() bool {
		once.Do(func() { ok = probeKeychain() })
		return ok
	}
}

// probeKeychain does a sentinel set→get→delete; any failure means the
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
// "keychain" or "file" — used by `pmox doctor`.
func ActiveBackend() string {
	switch storeMode() {
	case "keychain":
		return "keychain"
	case "file":
		return "file"
	default:
		if keychainAvailable() {
			return "keychain"
		}
		return "file"
	}
}

// Get retrieves the secret for account. Under auto it tries the keychain
// then the file, so a secret stored under either backend is found.
func Get(account string) (string, error) {
	switch storeMode() {
	case "keychain":
		if !keychainAvailable() {
			return "", errKeychainForced()
		}
		return kb.get(account)
	case "file":
		return fb.get(account)
	default:
		if keychainAvailable() {
			if v, err := kb.get(account); err == nil {
				return v, nil
			}
		}
		return fb.get(account)
	}
}

// Set stores secret under account. Under auto it writes to the keychain
// when available, otherwise the file.
func Set(account, secret string) error {
	switch storeMode() {
	case "keychain":
		if !keychainAvailable() {
			return errKeychainForced()
		}
		return kb.set(account, secret)
	case "file":
		return fb.set(account, secret)
	default:
		if keychainAvailable() {
			return kb.set(account, secret)
		}
		return fb.set(account, secret)
	}
}

// Remove deletes account from EVERY backend so no stray secret remains,
// regardless of where it was written. A missing entry in a backend is
// tolerated; ErrNotFound is returned only when no backend had it.
func Remove(account string) error {
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
	if keychainAvailable() {
		record(kb.remove(account))
	}
	record(fb.remove(account))
	if hardErr != nil {
		return hardErr
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, account)
	}
	return nil
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
