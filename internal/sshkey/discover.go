package sshkey

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// BootstrapKeyName is the file name of pmox's dedicated VM-bootstrap
// private key under ~/.ssh (its public half has a ".pub" suffix).
const BootstrapKeyName = "pmox_ed25519"

// ErrPubKeyMissing is returned by EnsureBootstrap when the bootstrap
// private key exists but its .pub companion does not. pmox never
// clobbers an existing private key, so the caller must resolve this.
var ErrPubKeyMissing = errors.New("private key exists but its public key is missing")

// ExpandHome expands a leading "~/" in p to the current user's home
// directory. Other paths (and a "~/" path when the home directory can't
// be resolved) are returned unchanged.
func ExpandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// FindPubKeys walks root and returns every "*.pub" file beneath it, in
// lexical walk order. Unreadable directories are skipped silently.
func FindPubKeys(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".pub") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// DefaultSuggestion returns current when set, else the first common
// default public key (id_ed25519.pub, then id_rsa.pub) that exists under
// sshDir, else "".
func DefaultSuggestion(current, sshDir string) string {
	if current != "" {
		return current
	}
	for _, c := range []string{
		filepath.Join(sshDir, "id_ed25519.pub"),
		filepath.Join(sshDir, "id_rsa.pub"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ResolvePubKey maps a selected key file to the public key pmox should
// store: a private key resolves to its adjacent .pub when that exists; a
// .pub (or anything else) is used as-is.
func ResolvePubKey(selected string) string {
	if strings.HasSuffix(selected, ".pub") {
		return selected
	}
	if _, err := os.Stat(selected + ".pub"); err == nil {
		return selected + ".pub"
	}
	return selected
}

// DefaultComment returns the comment pmox stamps on generated keys:
// "pmox@<hostname>", or plain "pmox" when the hostname is unavailable.
func DefaultComment() string {
	if hn, err := os.Hostname(); err == nil && hn != "" {
		return "pmox@" + hn
	}
	return "pmox"
}

// EnsureBootstrap returns the public-key path of pmox's dedicated
// bootstrap key (BootstrapKeyName) in sshDir, generating an ed25519
// keypair with the given comment when none exists. reused reports that
// an existing key was returned. An existing private key without its .pub
// yields ErrPubKeyMissing; the private key is never overwritten.
func EnsureBootstrap(sshDir, comment string) (pubPath string, reused bool, err error) {
	priv := filepath.Join(sshDir, BootstrapKeyName)
	pub := priv + ".pub"
	if _, err := os.Stat(priv); err == nil {
		if _, err := os.Stat(pub); err == nil {
			return pub, true, nil
		}
		return "", false, ErrPubKeyMissing
	}
	generated, err := Generate(priv, comment)
	if err != nil {
		return "", false, err
	}
	return generated, false, nil
}
