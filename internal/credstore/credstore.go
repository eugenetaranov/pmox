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
