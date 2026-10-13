package steps

import (
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
)

const testKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n"

func TestGitKeySent(t *testing.T) {
	e := shellEnv(t)
	e.Answers = &answers.Answers{GitKeyHosts: []string{"github.com", "git.acme.io:2222"}}
	e.Secrets = creds.New()
	e.Secrets.GitKey = []byte(testKey)
	put(t, e.Path("~/.ssh/config"), "Host *\n  ServerAliveInterval 30\n")
	put(t, e.Path("~/.ssh/known_hosts"), "10.0.0.5 ssh-ed25519 AAAA")
	st := step(t, catalog.Step{Type: "go:gitkey"})
	if check(t, e, st) {
		t.Fatal("Check = true before Apply")
	}
	apply(t, e, st)
	if got := read(t, e.Path(GitKeyPath)); got != testKey {
		t.Errorf("key = %q", got)
	}
	cfg := read(t, e.Path("~/.ssh/config"))
	want := gitBegin + "\nHost github.com\n  User git\n  IdentityFile ~/.ssh/devbox_git_key\n  IdentitiesOnly yes\n" +
		"Host git.acme.io\n  User git\n  IdentityFile ~/.ssh/devbox_git_key\n  IdentitiesOnly yes\n  Port 2222\n  StrictHostKeyChecking accept-new\n" +
		gitEnd + "\n\nHost *\n  ServerAliveInterval 30\n"
	if cfg != want {
		t.Errorf("config:\n%s\nwant:\n%s", cfg, want)
	}
	kh := read(t, e.Path("~/.ssh/known_hosts"))
	if !strings.HasPrefix(kh, "10.0.0.5 ssh-ed25519 AAAA\ngithub.com ssh-ed25519 ") {
		t.Errorf("known_hosts = %q", kh)
	}
	if !check(t, e, st) {
		t.Error("Check = false after Apply")
	}
	// The hosts change: the block is replaced, not added twice.
	e.Answers.GitKeyHosts = []string{"github.com"}
	apply(t, e, st)
	if cfg := read(t, e.Path("~/.ssh/config")); strings.Count(cfg, gitBegin) != 1 || strings.Contains(cfg, "acme") {
		t.Errorf("config after a change:\n%s", cfg)
	}
	// A different key sent later replaces it.
	e.Secrets.GitKey = []byte(strings.Replace(testKey, "AAAAA", "BBBBB", 1))
	if check(t, e, st) {
		t.Error("Check = true with a new key sent")
	}
}

func TestGitKeyGenerated(t *testing.T) {
	x := &fakeExec{}
	e := shellEnv(t)
	e.Exec = x
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	e.Answers = &answers.Answers{}
	apply(t, e, step(t, catalog.Step{Type: "go:gitkey"}))
	if got := x.commands(); len(got) != 1 || !strings.Contains(got[0], "ssh-keygen -q -t ed25519") {
		t.Errorf("ran %q", got)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "Deploy keys") {
		t.Errorf("notes = %q", notes)
	}
	if !strings.Contains(read(t, e.Path("~/.ssh/config")), "Host github.com\n") {
		t.Error("no github.com block")
	}
}
