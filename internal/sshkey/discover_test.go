package sshkey

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := ExpandHome("~/.ssh/id.pub"), filepath.Join(home, ".ssh", "id.pub"); got != want {
		t.Errorf("ExpandHome(~/...) = %q, want %q", got, want)
	}
	for _, p := range []string{"/abs/key", "rel/key", "~user/key", "~"} {
		if got := ExpandHome(p); got != p {
			t.Errorf("ExpandHome(%q) = %q, want unchanged", p, got)
		}
	}
}

func TestFindPubKeys(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "id_ed25519.pub"))
	touch(t, filepath.Join(dir, "id_ed25519"))
	touch(t, filepath.Join(dir, "sub", "work.pub"))
	touch(t, filepath.Join(dir, "config"))
	got := FindPubKeys(dir)
	want := []string{filepath.Join(dir, "id_ed25519.pub"), filepath.Join(dir, "sub", "work.pub")}
	if !slices.Equal(got, want) {
		t.Errorf("FindPubKeys = %q, want %q", got, want)
	}
	if got := FindPubKeys(filepath.Join(dir, "absent")); got != nil {
		t.Errorf("missing root: got %q, want nil", got)
	}
}

func TestDefaultSuggestion(t *testing.T) {
	dir := t.TempDir()
	if got := DefaultSuggestion("", dir); got != "" {
		t.Errorf("empty dir: got %q", got)
	}
	touch(t, filepath.Join(dir, "id_rsa.pub"))
	if got := DefaultSuggestion("", dir); got != filepath.Join(dir, "id_rsa.pub") {
		t.Errorf("rsa only: got %q", got)
	}
	touch(t, filepath.Join(dir, "id_ed25519.pub"))
	if got := DefaultSuggestion("", dir); got != filepath.Join(dir, "id_ed25519.pub") {
		t.Errorf("ed25519 preferred: got %q", got)
	}
	if got := DefaultSuggestion("/cur.pub", dir); got != "/cur.pub" {
		t.Errorf("current wins: got %q", got)
	}
}

func TestResolvePubKey(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "id_ed25519")
	touch(t, priv)
	touch(t, priv+".pub")
	if got := ResolvePubKey(priv); got != priv+".pub" {
		t.Errorf("private→pub: got %q", got)
	}
	if got := ResolvePubKey(priv + ".pub"); got != priv+".pub" {
		t.Errorf(".pub direct: got %q", got)
	}
	lone := filepath.Join(dir, "lone")
	if got := ResolvePubKey(lone); got != lone {
		t.Errorf("lone: got %q", got)
	}
}

func TestDefaultComment(t *testing.T) {
	if c := DefaultComment(); !strings.HasPrefix(c, "pmox") {
		t.Errorf("DefaultComment = %q", c)
	}
}

func TestEnsureBootstrap(t *testing.T) {
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	pub, reused, err := EnsureBootstrap(sshDir, "pmox@test")
	if err != nil || reused {
		t.Fatalf("first call: pub=%q reused=%v err=%v", pub, reused, err)
	}
	if pub != filepath.Join(sshDir, BootstrapKeyName+".pub") {
		t.Errorf("pub = %q", pub)
	}
	pub2, reused, err := EnsureBootstrap(sshDir, "pmox@test")
	if err != nil || !reused || pub2 != pub {
		t.Fatalf("reuse: pub=%q reused=%v err=%v", pub2, reused, err)
	}
	if err := os.Remove(pub); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureBootstrap(sshDir, "pmox@test"); !errors.Is(err, ErrPubKeyMissing) {
		t.Errorf("orphan private key: err = %v, want ErrPubKeyMissing", err)
	}
}
