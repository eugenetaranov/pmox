package server

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
)

const (
	urlA = "https://pve1.lan:8006/api2/json"
	urlB = "https://pve2.lan:8006/api2/json"
)

// setupCfg builds an in-memory config with N canonical server entries
// (max 2) and pre-seeds the mock keychain with their secrets.
func setupCfg(t *testing.T, n int) *config.Config {
	t.Helper()
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{}}
	if n >= 1 {
		cfg.Servers[urlA] = &config.Server{TokenID: "root@pam!a"}
		if err := credstore.Set(urlA, "secret-a"); err != nil {
			t.Fatalf("seed credstore A: %v", err)
		}
	}
	if n >= 2 {
		cfg.Servers[urlB] = &config.Server{TokenID: "root@pam!b"}
		if err := credstore.Set(urlB, "secret-b"); err != nil {
			t.Fatalf("seed credstore B: %v", err)
		}
	}
	return cfg
}

// pipeStdin returns an *os.File that is a pipe read-end — never a TTY.
// Close the returned cleanup function in a defer.
func pipeStdin(t *testing.T) (*os.File, func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	return r, func() {
		_ = r.Close()
		_ = w.Close()
	}
}

func baseOpts(t *testing.T, cfg *config.Config) (Options, func()) {
	t.Helper()
	stdin, cleanup := pipeStdin(t)
	return Options{
		Cfg:    cfg,
		Stdin:  stdin,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}, cleanup
}

func TestResolve_FlagTakesPrecedence(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	opts.Flag = urlB
	opts.Env = urlA // should be ignored

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.URL != urlB {
		t.Errorf("URL = %q, want %q", r.URL, urlB)
	}
	if r.Secret != "secret-b" {
		t.Errorf("Secret = %q, want secret-b", r.Secret)
	}
}

func TestResolve_EnvWhenFlagUnset(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	opts.Env = urlA

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.URL != urlA {
		t.Errorf("URL = %q, want %q", r.URL, urlA)
	}
}

func TestResolve_SingleConfigured(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.URL != urlA || r.Secret != "secret-a" {
		t.Errorf("resolved %+v", r)
	}
}

func TestResolve_ZeroServers(t *testing.T) {
	cfg := setupCfg(t, 0)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "pmox configure") {
		t.Errorf("missing hint in %q", err.Error())
	}
}

func TestResolve_NonTTYAmbiguity(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want ErrUserInput", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "--server") || !strings.Contains(msg, "PMOX_SERVER") {
		t.Errorf("error missing hints: %q", msg)
	}
	if !strings.Contains(msg, urlA) || !strings.Contains(msg, urlB) {
		t.Errorf("error missing candidates: %q", msg)
	}
}

func TestResolve_FlagMissListsCandidates(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	opts.Flag = "https://pve9.lan:8006/api2/json"

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want ErrUserInput", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no configured server matches") {
		t.Errorf("unexpected message: %q", msg)
	}
	if !strings.Contains(msg, urlA) {
		t.Errorf("missing candidate in %q", msg)
	}
}

func TestResolve_InvalidFlagShape(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	opts.Flag = "http://pve1.lan" // wrong scheme

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want ErrUserInput", err)
	}
}

func TestResolve_KeychainMiss(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {TokenID: "root@pam!a"},
	}}
	// Note: no credstore.Set — keychain empty.
	opts := Options{
		Cfg:    cfg,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	stdin, cleanup := pipeStdin(t)
	defer cleanup()
	opts.Stdin = stdin

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "re-run 'pmox configure'") {
		t.Errorf("missing hint in %q", err.Error())
	}
}

func TestResolve_ContextCancelled(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Resolve(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestMatchInput_Forms(t *testing.T) {
	cfg := setupCfg(t, 1) // only urlA
	cases := []struct {
		name  string
		input string
	}{
		{"full canonical", "https://pve1.lan:8006/api2/json"},
		{"no path", "https://pve1.lan:8006"},
		{"no port", "https://pve1.lan"},
		{"bare hostname", "pve1.lan"},
		{"hostname with port", "pve1.lan:8006"},
		{"trimmed whitespace", "  https://pve1.lan:8006/api2/json  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, srv, err := matchInput(tc.input, cfg)
			if err != nil {
				t.Fatalf("matchInput(%q): %v", tc.input, err)
			}
			if url != urlA {
				t.Errorf("url = %q, want %q", url, urlA)
			}
			if srv == nil {
				t.Fatalf("server is nil")
			}
		})
	}
}

func TestResolve_NodeSSH_PasswordMode(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: "password"},
		},
	}}
	if err := credstore.Set(urlA, "api"); err != nil {
		t.Fatal(err)
	}
	if err := credstore.SetNodeSSHPassword(urlA, "hunter2"); err != nil {
		t.Fatal(err)
	}
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !r.HasNodeSSH() {
		t.Fatalf("HasNodeSSH = false")
	}
	if r.NodeSSHUser != "root" || r.NodeSSHAuth != "password" || r.NodeSSHPassword != "hunter2" {
		t.Fatalf("resolved: %+v", r)
	}
	if r.NodeSSHKeyPath != "" || r.NodeSSHKeyPassphrase != "" {
		t.Fatalf("key fields should be empty: %+v", r)
	}
}

func TestResolve_NodeSSH_KeyModeUnencrypted(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: "key", KeyPath: "/path/key"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.NodeSSHAuth != "key" || r.NodeSSHKeyPath != "/path/key" || r.NodeSSHKeyPassphrase != "" {
		t.Fatalf("resolved: %+v", r)
	}
	if r.NodeSSHPassword != "" {
		t.Fatalf("password should be empty")
	}
}

func TestResolve_NodeSSH_KeyModeEncrypted(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: "key", KeyPath: "/k"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	_ = credstore.SetNodeSSHKeyPassphrase(urlA, "pp")
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.NodeSSHKeyPassphrase != "pp" {
		t.Fatalf("passphrase: %q", r.NodeSSHKeyPassphrase)
	}
}

func TestResolve_NodeSSH_LegacyNoFields(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.HasNodeSSH() {
		t.Fatalf("HasNodeSSH should be false for legacy record")
	}
}

func TestResolve_NodeSSH_PasswordMissingInKeyring(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: "password"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	// Intentionally no SetNodeSSHPassword — gap should be a hard error.
	opts, cleanup := baseOpts(t, cfg)
	defer cleanup()
	_, err := Resolve(context.Background(), opts)
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMatchInput_NoPrefixMatching(t *testing.T) {
	cfg := setupCfg(t, 1)
	_, _, err := matchInput("pve", cfg) // prefix only — should NOT match
	if err == nil {
		t.Fatal("expected error for prefix input, got nil")
	}
}
