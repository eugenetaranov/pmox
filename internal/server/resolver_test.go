package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
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

// baseOpts returns non-interactive options (no Pick) for cfg.
func baseOpts(cfg *config.Config) Options {
	return Options{Cfg: cfg}
}

func TestResolve_PickerChoosesContext(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts := baseOpts(cfg)
	var offered []Choice
	opts.Pick = func(title string, choices []Choice) (string, error) {
		offered = choices
		return urlB, nil
	}

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlB || r.Source != "interactive picker" {
		t.Errorf("got URL=%q Source=%q, want %q via interactive picker", r.URL, r.Source, urlB)
	}
	if len(offered) != 2 {
		t.Errorf("offered %d choices, want 2", len(offered))
	}
}

// TestResolve_PickerAbortNeverFallsBack pins the Ctrl-C fix: an aborted
// context picker must surface its error and never resolve to the first
// context (which would run e.g. `pmox delete` against it).
func TestResolve_PickerAbortNeverFallsBack(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts := baseOpts(cfg)
	opts.Pick = func(string, []Choice) (string, error) { return "", tui.ErrAborted }

	r, err := Resolve(context.Background(), opts)
	if !errors.Is(err, tui.ErrAborted) {
		t.Fatalf("err = %v, want picker abort error", err)
	}
	if r != nil {
		t.Errorf("resolved %q after abort, want nil", r.URL)
	}
}

func TestResolve_PickerUnknownValue(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts := baseOpts(cfg)
	opts.Pick = func(string, []Choice) (string, error) { return "https://nope:8006/api2/json", nil }

	if _, err := Resolve(context.Background(), opts); err == nil {
		t.Fatal("expected error for unknown picker value")
	}
}

func TestResolve_FlagTakesPrecedence(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts := baseOpts(cfg)
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
	opts := baseOpts(cfg)
	opts.Env = urlA

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.URL != urlA {
		t.Errorf("URL = %q, want %q", r.URL, urlA)
	}
}

func TestResolve_CurrentContextWhenSet(t *testing.T) {
	cfg := setupCfg(t, 2)
	// pve2's derived context name is its host, "pve2.lan".
	cfg.CurrentContext = "pve2.lan"
	opts := baseOpts(cfg)

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlB || r.Secret != "secret-b" {
		t.Errorf("resolved %q (secret %q), want %q", r.URL, r.Secret, urlB)
	}
	if r.Source != "current context" {
		t.Errorf("Source = %q, want 'current context'", r.Source)
	}
}

func TestResolve_ContextFlagByName(t *testing.T) {
	cfg := setupCfg(t, 2)
	cfg.Servers[urlB].Name = "lab" // explicit context name
	opts := baseOpts(cfg)
	opts.Context = "lab"

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlB {
		t.Errorf("resolved %q, want %q (--context lab)", r.URL, urlB)
	}
	if r.Source != "--context flag" {
		t.Errorf("Source = %q, want '--context flag'", r.Source)
	}
}

func TestResolve_ServerFlagAcceptsContextName(t *testing.T) {
	cfg := setupCfg(t, 2)
	cfg.Servers[urlA].Name = "prod"
	opts := baseOpts(cfg)
	opts.Flag = "prod" // --server accepts a context name too

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlA {
		t.Errorf("resolved %q, want %q (--server prod)", r.URL, urlA)
	}
}

func TestResolve_FlagOverridesCurrentContext(t *testing.T) {
	cfg := setupCfg(t, 2)
	cfg.CurrentContext = "pve2.lan"
	opts := baseOpts(cfg)
	opts.Flag = urlA // explicit flag beats the current context

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlA {
		t.Errorf("resolved %q, want %q (flag beats current context)", r.URL, urlA)
	}
}

func TestResolve_StaleCurrentContextIgnored(t *testing.T) {
	// A current context naming a server that no longer exists must not
	// hard-fail; with a single remaining server the ladder falls through.
	cfg := setupCfg(t, 1)
	cfg.CurrentContext = "gone"
	opts := baseOpts(cfg)

	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != urlA {
		t.Errorf("resolved %q, want %q (stale current context ignored)", r.URL, urlA)
	}
}

func TestResolve_SingleConfigured(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts := baseOpts(cfg)

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
	opts := baseOpts(cfg)

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "pmox init") {
		t.Errorf("missing hint in %q", err.Error())
	}
}

func TestResolve_NonTTYAmbiguity(t *testing.T) {
	cfg := setupCfg(t, 2)
	opts := baseOpts(cfg)

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
	opts := baseOpts(cfg)
	opts.Flag = "https://pve9.lan:8006/api2/json"

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want ErrUserInput", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no configured context or server matches") {
		t.Errorf("unexpected message: %q", msg)
	}
	if !strings.Contains(msg, urlA) {
		t.Errorf("missing candidate in %q", msg)
	}
}

func TestResolve_InvalidFlagShape(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts := baseOpts(cfg)
	opts.Flag = "https://[oops" // unparseable URL (bad IPv6 bracket)

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
		Cfg: cfg,
	}

	_, err := Resolve(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "re-run 'pmox init'") {
		t.Errorf("missing hint in %q", err.Error())
	}
}

func TestResolve_ContextCancelled(t *testing.T) {
	cfg := setupCfg(t, 1)
	opts := baseOpts(cfg)

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
	opts := baseOpts(cfg)
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
	opts := baseOpts(cfg)
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
	opts := baseOpts(cfg)
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
	opts := baseOpts(cfg)
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
	opts := baseOpts(cfg)
	_, err := Resolve(context.Background(), opts)
	if !errors.Is(err, exitcode.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestResolve_NodeSSH_UnknownAuthDeferredToNodeSSHUsers(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: "kerberos"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	opts := baseOpts(cfg)
	// Commands that don't use node SSH still resolve.
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.HasNodeSSH() {
		t.Error("HasNodeSSH = true for an unknown auth mode")
	}
	// Node-SSH users get the recorded problem.
	err = r.RequireNodeSSH("launch")
	if !errors.Is(err, exitcode.ErrUserInput) || !strings.Contains(err.Error(), "kerberos") {
		t.Fatalf("RequireNodeSSH: want ErrUserInput naming the auth mode, got %v", err)
	}
}

func TestResolve_NodeSSH_PassphraseLookupErrorIsNotFatal(t *testing.T) {
	keyring.MockInit()
	orig := getNodeSSHKeyPassphrase
	getNodeSSHKeyPassphrase = func(string) (string, error) { return "", errors.New("keychain is locked") }
	t.Cleanup(func() { getNodeSSHKeyPassphrase = orig })
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{User: "root", Auth: config.AuthKey, KeyPath: "/k"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	r, err := Resolve(context.Background(), baseOpts(cfg))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.NodeSSHKeyPassphrase != "" || !r.HasNodeSSH() {
		t.Fatalf("want key auth without passphrase, got %+v", r)
	}
	if err := r.RequireNodeSSH("launch"); err != nil {
		t.Fatalf("RequireNodeSSH: %v", err)
	}
}

func TestRequireNodeSSH_Unconfigured(t *testing.T) {
	err := (&Resolved{URL: urlA}).RequireNodeSSH("clone")
	if !errors.Is(err, exitcode.ErrUserInput) || !strings.Contains(err.Error(), "clone needs SSH access") {
		t.Fatalf("want ErrUserInput naming the command, got %v", err)
	}
}

func TestResolve_NodeSSH_DefaultUserRoot(t *testing.T) {
	keyring.MockInit()
	cfg := &config.Config{Servers: map[string]*config.Server{
		urlA: {
			TokenID: "t@pam!a",
			NodeSSH: &config.NodeSSH{Auth: config.AuthKey, KeyPath: "/k"},
		},
	}}
	_ = credstore.Set(urlA, "api")
	opts := baseOpts(cfg)
	r, err := Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.NodeSSHUser != "root" {
		t.Fatalf("NodeSSHUser = %q, want root", r.NodeSSHUser)
	}
}

func TestMatchInput_NoPrefixMatching(t *testing.T) {
	cfg := setupCfg(t, 1)
	_, _, err := matchInput("pve", cfg) // prefix only — should NOT match
	if err == nil {
		t.Fatal("expected error for prefix input, got nil")
	}
}
