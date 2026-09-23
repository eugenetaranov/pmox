package sshkey

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateWritesUsableKeypairWithPerms(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "keys", "pmox_ed25519")

	pubPath, err := Generate(priv, "pmox@test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if pubPath != priv+".pub" {
		t.Errorf("pubPath = %q, want %q", pubPath, priv+".pub")
	}

	// Permissions: private 0600, public 0644, dir 0700.
	if fi, _ := os.Stat(priv); fi.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(pubPath); fi.Mode().Perm() != 0o644 {
		t.Errorf("public key mode = %v, want 0644", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(priv)); fi.Mode().Perm() != 0o700 {
		t.Errorf("key dir mode = %v, want 0700", fi.Mode().Perm())
	}

	// Private key parses.
	privBytes, _ := os.ReadFile(priv)
	if _, err := ssh.ParsePrivateKey(privBytes); err != nil {
		t.Errorf("private key does not parse: %v", err)
	}
	// Public key parses and carries the comment.
	pubBytes, _ := os.ReadFile(pubPath)
	_, comment, _, _, err := ssh.ParseAuthorizedKey(pubBytes)
	if err != nil {
		t.Fatalf("public key does not parse: %v", err)
	}
	if comment != "pmox@test" {
		t.Errorf("comment = %q, want pmox@test", comment)
	}
}

func TestGenerateNoClobber(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "existing")
	if err := os.WriteFile(priv, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(priv, "x"); err == nil {
		t.Fatal("want error when key already exists")
	}
	// Original content untouched.
	got, _ := os.ReadFile(priv)
	if string(got) != "do not touch" {
		t.Errorf("existing key was modified: %q", got)
	}
}
