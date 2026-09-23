// Package sshkey generates SSH keypairs for pmox's VM-bootstrap use.
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Generate creates an ed25519 OpenSSH keypair, writing the private key to
// privPath (mode 0600) and the public key to privPath+".pub" (mode 0644),
// creating the parent directory (mode 0700) if needed. It refuses to
// overwrite an existing private key. It returns the public key path.
func Generate(privPath, comment string) (pubPath string, err error) {
	if privPath == "" {
		return "", fmt.Errorf("empty key path")
	}
	if _, statErr := os.Stat(privPath); statErr == nil {
		return "", fmt.Errorf("key already exists: %s", privPath)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Private key in OpenSSH PEM format. MarshalPrivateKey wants a pointer
	// for ed25519 keys.
	block, err := ssh.MarshalPrivateKey(&priv, comment)
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(block)

	// Public key in authorized_keys form; append the comment to the line.
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("build public key: %w", err)
	}
	pubLine := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
	if comment != "" {
		pubLine += " " + comment
	}
	pubLine += "\n"

	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return "", fmt.Errorf("create key directory: %w", err)
	}
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return "", fmt.Errorf("write private key: %w", err)
	}
	pubPath = privPath + ".pub"
	if err := os.WriteFile(pubPath, []byte(pubLine), 0o644); err != nil { //nolint:gosec // public key is world-readable by convention
		// Clean up the private key so a failed generation leaves no half state.
		_ = os.Remove(privPath)
		return "", fmt.Errorf("write public key: %w", err)
	}
	return pubPath, nil
}
