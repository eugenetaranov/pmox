// Package credstore wraps github.com/zalando/go-keyring to persist PVE
// API token secrets in the system keychain.
package credstore

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/zalando/go-keyring"
)

const service = "pmox"

// Suffixes appended to a canonical server URL to form the keyring
// account for secrets other than the main API token. These live beside
// the bare URL entry (which holds the API token secret).
const (
	suffixSSHPassword      = "#node_ssh_password"
	suffixSSHKeyPassphrase = "#node_ssh_key_passphrase"
)

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

// ErrNotFound is returned by Get when no secret exists for the URL.
var ErrNotFound = errors.New("token not found in keychain")

// Get retrieves the secret stored for url.
func Get(url string) (string, error) {
	secret, err := keyring.Get(service, url)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, url)
		}
		return "", wrapKeychainErr(err)
	}
	return secret, nil
}

// Set stores secret under url in the system keychain.
func Set(url, secret string) error {
	if err := keyring.Set(service, url, secret); err != nil {
		return wrapKeychainErr(err)
	}
	return nil
}

// Remove deletes the entry for url. Missing entries return ErrNotFound.
func Remove(url string) error {
	if err := keyring.Delete(service, url); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, url)
		}
		return wrapKeychainErr(err)
	}
	return nil
}

func wrapKeychainErr(err error) error {
	if runtime.GOOS == "linux" {
		return fmt.Errorf("system keychain unavailable: %w; pmox requires gnome-keyring or KWallet on Linux", err)
	}
	return fmt.Errorf("keychain error: %w", err)
}
