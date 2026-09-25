package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func init() {
	keyring.MockInit()
}

// fakePrompter is a scripted prompter driver for tests.
type fakePrompter struct {
	inputs  []string
	secrets []string
	idx     int
	sidx    int
	out     bytes.Buffer
	err     bytes.Buffer
}

func (f *fakePrompter) Prompt(msg string) (string, error) {
	f.out.WriteString(msg)
	if f.idx >= len(f.inputs) {
		return "", errors.New("fakePrompter: no more inputs")
	}
	v := f.inputs[f.idx]
	f.idx++
	return v, nil
}

func (f *fakePrompter) PromptSecret(msg string) (string, error) {
	f.out.WriteString(msg)
	if f.sidx >= len(f.secrets) {
		return "", errors.New("fakePrompter: no more secrets")
	}
	v := f.secrets[f.sidx]
	f.sidx++
	return v, nil
}

func (f *fakePrompter) Printf(format string, args ...interface{}) {
	fmt.Fprintf(&f.out, format, args...)
}

func (f *fakePrompter) Errf(format string, args ...interface{}) {
	fmt.Fprintf(&f.err, format, args...)
}

// In/Out feed host-key pinning a throwaway "yes"; tests stub the seam.
func (f *fakePrompter) In() io.Reader  { return strings.NewReader("yes\n") }
func (f *fakePrompter) Out() io.Writer { return io.Discard }

func TestListEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := &fakePrompter{}
	if err := runList(p); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if got := p.out.String(); got != "no servers configured\n" {
		t.Errorf("out = %q", got)
	}
}

func TestListMultiple(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{Servers: map[string]*config.Server{
		"https://b.example:8006/api2/json": {TokenID: "x@y!z"},
		"https://a.example:8006/api2/json": {TokenID: "x@y!z"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := &fakePrompter{}
	if err := runList(p); err != nil {
		t.Fatalf("runList: %v", err)
	}
	want := "https://a.example:8006/api2/json\nhttps://b.example:8006/api2/json\n"
	if got := p.out.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoveExisting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://pve.home.lan:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "x@y!z"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_ = credstore.Set(url, "sekret")

	p := &fakePrompter{}
	if err := runRemove(p, url); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	// Config file no longer has it
	loaded, _ := config.Load()
	if _, ok := loaded.Servers[url]; ok {
		t.Error("server still in config")
	}
	// Keychain no longer has it
	if _, err := credstore.Get(url); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("want ErrNotFound after remove, got %v", err)
	}
	if !strings.Contains(p.out.String(), "removed "+url) {
		t.Errorf("out = %q", p.out.String())
	}
}

func TestRemoveMissingReturnsNotFound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := &fakePrompter{}
	err := runRemove(p, "https://nope.example:8006/api2/json")
	if !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("want credstore.ErrNotFound, got %v", err)
	}
}

func TestRemoveToleratesOrphanKeychain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://orphan.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "x@y!z"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// No credstore.Set — orphan
	p := &fakePrompter{}
	if err := runRemove(p, url); err != nil {
		t.Fatalf("runRemove orphan: %v", err)
	}
}

func TestRemoveNonCanonicalURLIsCanonicalized(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	canonical := "https://pve.home.lan:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{canonical: {TokenID: "x@y!z"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := &fakePrompter{}
	if err := runRemove(p, "HTTPS://PVE.HOME.LAN/api2/json/"); err != nil {
		t.Fatalf("runRemove uncanonical: %v", err)
	}
}

// stubProbe overrides probeEndpoint/interactiveFn for a test, returning
// the given statuses in sequence (last one repeats), and restores them.
func stubProbe(t *testing.T, interactive bool, statuses ...pveclient.ReachStatus) {
	t.Helper()
	origProbe, origInteractive := probeEndpoint, interactiveFn
	i := 0
	probeEndpoint = func(_ context.Context, _ string, _ bool) (pveclient.ReachStatus, error) {
		s := statuses[len(statuses)-1]
		if i < len(statuses) {
			s = statuses[i]
		}
		i++
		return s, nil
	}
	interactiveFn = func() bool { return interactive }
	t.Cleanup(func() { probeEndpoint, interactiveFn = origProbe, origInteractive })
}

func TestPromptReachableURLRetriesThenSucceeds(t *testing.T) {
	stubProbe(t, true, pveclient.ReachUnreachable, pveclient.Reachable)
	p := &fakePrompter{inputs: []string{"10.0.0.9", "pve.home.lan"}}
	got, insecure, err := promptReachableURL(context.Background(), p)
	if err != nil {
		t.Fatalf("promptReachableURL: %v", err)
	}
	if got != "https://pve.home.lan:8006/api2/json" {
		t.Errorf("got %q", got)
	}
	if insecure {
		t.Errorf("insecure = true, want false")
	}
}

func TestPromptReachableURLBlankAborts(t *testing.T) {
	stubProbe(t, true, pveclient.Reachable)
	p := &fakePrompter{inputs: []string{""}}
	if _, _, err := promptReachableURL(context.Background(), p); err == nil {
		t.Fatal("want error on blank URL")
	}
}

func TestPromptReachableURLNonInteractiveFailFast(t *testing.T) {
	stubProbe(t, false, pveclient.ReachUnreachable)
	// Only one input: a fail-fast path must not re-prompt (a second read
	// would return the fakePrompter's "no more inputs" error instead).
	p := &fakePrompter{inputs: []string{"pve.home.lan"}}
	if _, _, err := promptReachableURL(context.Background(), p); err == nil {
		t.Fatal("want error when non-interactive and unreachable")
	}
}

func TestPromptReachableURLTLSFallback(t *testing.T) {
	// Strict probe reports TLS untrusted; the insecure retry reaches it.
	stubProbe(t, true, pveclient.ReachTLSUntrusted, pveclient.Reachable)
	p := &fakePrompter{inputs: []string{"pve.home.lan"}}
	got, insecure, err := promptReachableURL(context.Background(), p)
	if err != nil {
		t.Fatalf("promptReachableURL: %v", err)
	}
	if !insecure {
		t.Errorf("insecure = false, want true after TLS fallback")
	}
	if got != "https://pve.home.lan:8006/api2/json" {
		t.Errorf("got %q", got)
	}
}

func TestPromptTokenIDValidationRetries(t *testing.T) {
	p := &fakePrompter{inputs: []string{"pmox", "pmox@pve", "pmox@pve!homelab"}}
	got, err := promptTokenID(p)
	if err != nil {
		t.Fatalf("promptTokenID: %v", err)
	}
	if got != "pmox@pve!homelab" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(p.err.String(), "user@realm!tokenname") {
		t.Errorf("stderr missing format hint: %q", p.err.String())
	}
}

func TestPromptTokenIDExhausted(t *testing.T) {
	p := &fakePrompter{inputs: []string{"bad", "still-bad", "nope"}}
	_, err := promptTokenID(p)
	if err == nil {
		t.Fatal("want error")
	}
}

func TestPromptSecretRejectsEmpty(t *testing.T) {
	p := &fakePrompter{secrets: []string{"", "real-secret"}}
	got, err := promptSecret(p)
	if err != nil {
		t.Fatalf("promptSecret: %v", err)
	}
	if got != "real-secret" {
		t.Errorf("got %q", got)
	}
}

func TestValidateCredentialsStrictSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2"}})
	}))
	t.Cleanup(srv.Close)
	p := &fakePrompter{}
	insecure, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret", false, "")
	if err != nil {
		t.Fatalf("validateCredentials: %v", err)
	}
	if insecure {
		t.Error("strict success should leave insecure=false")
	}
}

func TestValidateCredentialsTLSFallback(t *testing.T) {
	// Start a TLS server with a self-signed cert.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2"}})
	}))
	t.Cleanup(srv.Close)
	p := &fakePrompter{}
	insecure, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret", false, "")
	if err != nil {
		t.Fatalf("validateCredentials: %v", err)
	}
	if !insecure {
		t.Error("TLS fallback should set insecure=true")
	}
	if !strings.Contains(p.err.String(), "WARNING: TLS verification failed") {
		t.Errorf("missing warning; stderr: %q", p.err.String())
	}
}

func TestValidateCredentialsUnauthorizedReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	p := &fakePrompter{}
	_, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret", false, "")
	if !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

// Re-running init against a server whose certificate is already pinned
// must enforce that pin: a swapped certificate fails the handshake
// before the token (or login password) is sent.
func TestInitReconfigureEnforcesStoredPin(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"data":{"version":"8.2"}}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{Servers: map[string]*config.Server{
		srv.URL: {TokenID: "a@pam!x", Insecure: true, TLSPinSHA256: strings.Repeat("ab", 32)},
	}}
	pin := storedPinFor(cfg, srv.URL)
	if pin == "" {
		t.Fatal("storedPinFor returned no pin for a pinned insecure server")
	}
	if got := storedPinFor(cfg, "https://other:8006/api2/json"); got != "" {
		t.Errorf("unknown server pin = %q, want empty (TOFU)", got)
	}

	p := &fakePrompter{}
	if _, err := validateCredentials(context.Background(), p, srv.URL, "a@pam!x", "sekret", true, pin); !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("validateCredentials: err = %v, want ErrTLSVerificationFailed", err)
	}
	if _, err := newInitClient(srv.URL, "a@pam!x", "sekret", true, pin).GetVersion(context.Background()); !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("discovery client: err = %v, want ErrTLSVerificationFailed", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server received %d request(s) despite pin mismatch", n)
	}
}

func TestPickOneAutoSingleOption(t *testing.T) {
	p := &fakePrompter{}
	opts := []huh.Option[string]{huh.NewOption("pve (online)", "pve")}
	got, err := pickOneAuto(p, "Default node", opts, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "pve" {
		t.Errorf("got %q, want pve", got)
	}
	if !strings.Contains(p.out.String(), "Default node: pve (online)") {
		t.Errorf("single option not reported; stdout: %q", p.out.String())
	}
}

func TestPickOneAutoEmptyUsesFallback(t *testing.T) {
	p := &fakePrompter{}
	got, err := pickOneAuto(p, "Default node", nil, "fallback-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback-node" {
		t.Errorf("got %q, want fallback-node", got)
	}
}

func TestGenerateBootstrapKeyCreatesAndReuses(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	p := &fakePrompter{}

	pub, err := generateBootstrapKey(p, sshDir, home)
	if err != nil {
		t.Fatalf("generateBootstrapKey: %v", err)
	}
	if pub != filepath.Join(sshDir, "pmox_ed25519.pub") {
		t.Errorf("pub = %q", pub)
	}
	if fi, statErr := os.Stat(filepath.Join(sshDir, "pmox_ed25519")); statErr != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("private key missing or wrong perms: %v", statErr)
	}
	if !strings.Contains(p.out.String(), "generated new SSH key") {
		t.Errorf("missing generated message: %q", p.out.String())
	}

	// Second call reuses without clobbering.
	p2 := &fakePrompter{}
	pub2, err := generateBootstrapKey(p2, sshDir, home)
	if err != nil {
		t.Fatalf("generateBootstrapKey reuse: %v", err)
	}
	if pub2 != pub {
		t.Errorf("reuse returned %q, want %q", pub2, pub)
	}
	if !strings.Contains(p2.out.String(), "reusing existing pmox key") {
		t.Errorf("missing reuse message: %q", p2.out.String())
	}
}

func TestPromptSSHKeyNonInteractiveUsesSuggestion(t *testing.T) {
	// interactiveFn=false → text fallback; blank input accepts the suggested
	// key (which must exist on disk to pass the readability check).
	origInteractive := interactiveFn
	interactiveFn = func() bool { return false }
	t.Cleanup(func() { interactiveFn = origInteractive })

	key := writePubKey(t, "ssh-ed25519 AAAA test@host\n")
	p := &fakePrompter{inputs: []string{""}}
	got, err := promptSSHKey(p, key)
	if err != nil {
		t.Fatalf("promptSSHKey: %v", err)
	}
	if got != key {
		t.Errorf("got %q, want %q", got, key)
	}
}

func TestConfiguredUser(t *testing.T) {
	cfg := &config.Config{Servers: map[string]*config.Server{
		"https://pve.example:8006/api2/json":   {User: "deploy"},
		"https://noone.example:8006/api2/json": {},
	}}
	cases := map[string]string{
		"https://pve.example:8006/api2/json":     "deploy",
		"https://noone.example:8006/api2/json":   "",
		"https://unknown.example:8006/api2/json": "",
	}
	for url, want := range cases {
		if got := configuredUser(cfg, url); got != want {
			t.Errorf("configuredUser(%q) = %q, want %q", url, got, want)
		}
	}
}

// TestPromptDefaultUser guards the reconfigure-friendly behavior: the
// prompt's bracketed default is whatever the caller already knows about
// (a previously-configured or in-progress value), not always "ubuntu",
// and a blank reply keeps that default rather than resetting to ubuntu.
func TestPromptDefaultUser(t *testing.T) {
	t.Run("no prior value suggests ubuntu", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{""}}
		got, err := promptDefaultUser(p, "")
		if err != nil || got != "ubuntu" {
			t.Fatalf("got %q, %v; want ubuntu", got, err)
		}
		if !strings.Contains(p.out.String(), "[ubuntu]") {
			t.Errorf("prompt = %q, want it to show [ubuntu]", p.out.String())
		}
	})
	t.Run("prior value is suggested and kept on blank reply", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{""}}
		got, err := promptDefaultUser(p, "deploy")
		if err != nil || got != "deploy" {
			t.Fatalf("got %q, %v; want deploy (the prior value)", got, err)
		}
		if !strings.Contains(p.out.String(), "[deploy]") {
			t.Errorf("prompt = %q, want it to show [deploy], not ubuntu", p.out.String())
		}
	})
	t.Run("explicit reply overrides the suggested default", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{"alice"}}
		got, err := promptDefaultUser(p, "deploy")
		if err != nil || got != "alice" {
			t.Fatalf("got %q, %v; want alice", got, err)
		}
	})
}

func TestOverwritePromptRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProbe(t, true, pveclient.Reachable)
	url := "https://pve.home.lan:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "original@pve!orig"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &fakePrompter{inputs: []string{url, "n"}}
	// Exercise the linear (non-interactive) flow's overwrite behavior directly.
	if err := runInteractiveLinear(context.Background(), p); err != nil {
		t.Fatalf("runInteractiveLinear: %v", err)
	}
	// Config still has original token ID
	loaded, _ := config.Load()
	if loaded.Servers[url].TokenID != "original@pve!orig" {
		t.Errorf("config was modified despite reject")
	}
	if !strings.Contains(p.out.String(), "aborted; no changes") {
		t.Errorf("out missing abort message: %q", p.out.String())
	}
}

// writePubKey drops a fake public key on disk and returns its path —
// used by the cloud-init regen tests so readSSHKey() has something
// real to load.
func writePubKey(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "id.pub")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWriteInitialCloudInit_FirstWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyPath := writePubKey(t, "ssh-ed25519 AAAA test@host\n")
	p := &fakePrompter{}
	writeInitialCloudInit(p, "https://pve.example:8006/api2/json", "ubuntu", keyPath)

	path, _ := config.CloudInitPath("https://pve.example:8006/api2/json")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cloud-init: %v", err)
	}
	if !bytes.Contains(got, []byte("ssh-ed25519 AAAA test@host")) {
		t.Errorf("cloud-init missing pubkey: %s", got)
	}
	if !bytes.Contains(got, []byte("name: ubuntu")) {
		t.Errorf("cloud-init missing user: %s", got)
	}
	if !strings.Contains(p.out.String(), "wrote cloud-init template") {
		t.Errorf("out missing confirmation: %q", p.out.String())
	}
}

func TestWriteInitialCloudInit_DoesNotOverwrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyPath := writePubKey(t, "ssh-ed25519 AAAA test@host\n")

	path, _ := config.CloudInitPath("https://pve.example:8006/api2/json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("# my custom file\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &fakePrompter{}
	writeInitialCloudInit(p, "https://pve.example:8006/api2/json", "ubuntu", keyPath)

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Errorf("file was modified: %s", got)
	}
	if !strings.Contains(p.out.String(), "not overwriting") {
		t.Errorf("out missing idempotent message: %q", p.out.String())
	}
}

func TestRegenCloudInit_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyPath := writePubKey(t, "ssh-ed25519 AAAA regen@host\n")
	url := "https://pve.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{
		url: {TokenID: "t@pve!x", SSHPubkey: keyPath, User: "ubuntu"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	p := &fakePrompter{}
	if err := runRegenCloudInit(p); err != nil {
		t.Fatalf("runRegenCloudInit: %v", err)
	}
	path, _ := config.CloudInitPath(url)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cloud-init: %v", err)
	}
	if !bytes.Contains(got, []byte("ssh-ed25519 AAAA regen@host")) {
		t.Errorf("missing pubkey: %s", got)
	}
}

func TestRegenCloudInit_Overwrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyPath := writePubKey(t, "ssh-ed25519 AAAA regen@host\n")
	url := "https://pve.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{
		url: {TokenID: "t@pve!x", SSHPubkey: keyPath, User: "ubuntu"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path, _ := config.CloudInitPath(url)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &fakePrompter{inputs: []string{"y"}}
	if err := runRegenCloudInit(p); err != nil {
		t.Fatalf("runRegenCloudInit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if bytes.Equal(got, []byte("old\n")) {
		t.Errorf("file was not overwritten")
	}
	if !bytes.Contains(got, []byte("ssh-ed25519 AAAA regen@host")) {
		t.Errorf("missing pubkey: %s", got)
	}
}

func TestRegenCloudInit_Abort(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyPath := writePubKey(t, "ssh-ed25519 AAAA regen@host\n")
	url := "https://pve.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{
		url: {TokenID: "t@pve!x", SSHPubkey: keyPath, User: "ubuntu"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path, _ := config.CloudInitPath(url)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("keep me\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &fakePrompter{inputs: []string{"n"}}
	if err := runRegenCloudInit(p); err != nil {
		t.Fatalf("runRegenCloudInit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Errorf("file was modified: %s", got)
	}
	if !strings.Contains(p.out.String(), "aborted") {
		t.Errorf("out missing abort message: %q", p.out.String())
	}
}

// fakeSnippetClient is a minimal in-memory snippetStoragePicker for the
// pickSnippetStorage tests. It records UpdateStorageContent calls so
// tests can assert the sent content list.
type fakeSnippetClient struct {
	pools     []pveclient.Storage
	listErr   error
	updateErr error

	updatedStorage string
	updatedContent []string
}

func (f *fakeSnippetClient) ListStorage(_ context.Context, _ string) ([]pveclient.Storage, error) {
	return f.pools, f.listErr
}

func (f *fakeSnippetClient) UpdateStorageContent(_ context.Context, storage string, content []string) error {
	f.updatedStorage = storage
	f.updatedContent = content
	return f.updateErr
}

func TestPickSnippetStorage_SingleMatch(t *testing.T) {
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "vm-data", Type: "lvmthin", Content: "images,rootdir"},
		{Storage: "local", Type: "dir", Content: "iso,vztmpl,snippets"},
	}}
	p := &fakePrompter{}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "local" {
		t.Errorf("got %q, want local", got)
	}
	if fc.updatedStorage != "" {
		t.Errorf("unexpected UpdateStorageContent call: %s", fc.updatedStorage)
	}
}

func TestPickSnippetStorage_MultiMatchUsesPicker(t *testing.T) {
	prev := selectSnippetStorageFn
	defer func() { selectSnippetStorageFn = prev }()
	var gotTitle string
	selectSnippetStorageFn = func(title string, _ []huh.Option[string], _ string) (string, error) {
		gotTitle = title
		return "nfs-shared", nil
	}
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso,snippets"},
		{Storage: "nfs-shared", Type: "nfs", Content: "snippets,backup"},
	}}
	p := &fakePrompter{}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nfs-shared" {
		t.Errorf("got %q, want nfs-shared", got)
	}
	if gotTitle != "Snippet storage" {
		t.Errorf("title = %q", gotTitle)
	}
}

func TestPickSnippetStorage_ZeroMatchEnableYes(t *testing.T) {
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "vm-data", Type: "lvmthin", Content: "images,rootdir"},
		{Storage: "local", Type: "dir", Content: "iso,vztmpl"},
	}}
	p := &fakePrompter{inputs: []string{"y"}}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "local" {
		t.Errorf("got %q, want local", got)
	}
	if fc.updatedStorage != "local" {
		t.Errorf("UpdateStorageContent storage = %q, want local", fc.updatedStorage)
	}
	if !slices.Contains(fc.updatedContent, "snippets") || !slices.Contains(fc.updatedContent, "iso") || !slices.Contains(fc.updatedContent, "vztmpl") {
		t.Errorf("updatedContent = %v, want includes iso, vztmpl, snippets", fc.updatedContent)
	}
}

func TestPickSnippetStorage_ZeroMatchEnableEnterDefaultsYes(t *testing.T) {
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso"},
	}}
	p := &fakePrompter{inputs: []string{""}}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "local" {
		t.Errorf("got %q, want local (Enter defaults to yes)", got)
	}
	if fc.updatedStorage != "local" {
		t.Errorf("UpdateStorageContent storage = %q, want local", fc.updatedStorage)
	}
}

func TestPickSnippetStorage_ZeroMatchEnableNo(t *testing.T) {
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "local", Type: "dir", Content: "iso"},
	}}
	p := &fakePrompter{inputs: []string{"n"}}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty (declined)", got)
	}
	if fc.updatedStorage != "" {
		t.Errorf("unexpected update call after decline: %s", fc.updatedStorage)
	}
	if !strings.Contains(p.err.String(), "/etc/pve/storage.cfg") {
		t.Errorf("missing manual remediation in stderr: %q", p.err.String())
	}
}

func TestPickSnippetStorage_ZeroMatchNoCapableStorage(t *testing.T) {
	fc := &fakeSnippetClient{pools: []pveclient.Storage{
		{Storage: "vm-data", Type: "lvmthin", Content: "images,rootdir"},
	}}
	p := &fakePrompter{}
	got, err := pickSnippetStorage(context.Background(), p, fc, "pve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if !strings.Contains(p.err.String(), "/etc/pve/storage.cfg") {
		t.Errorf("missing manual remediation in stderr: %q", p.err.String())
	}
}

// stubRepin stubs the certificate probe, interactivity and the re-pin
// confirmation for resolveInitPin tests. It returns a pointer to the
// number of times the confirmation was shown.
func stubRepin(t *testing.T, fp string, fpErr error, interactive, answer bool) *int {
	t.Helper()
	withStubbedFingerprint(t, fp, fpErr)
	origInteractive, origConfirm := interactiveFn, confirmRepinFn
	asked := 0
	interactiveFn = func() bool { return interactive }
	confirmRepinFn = func(_ string, defaultYes bool) (bool, error) {
		asked++
		if defaultYes {
			t.Error("re-pin confirmation must default to No")
		}
		return answer, nil
	}
	t.Cleanup(func() { interactiveFn, confirmRepinFn = origInteractive, origConfirm })
	return &asked
}

func repinCfg(pin string) (*config.Config, string) {
	url := "https://pve.home.lan:8006/api2/json"
	return &config.Config{Servers: map[string]*config.Server{
		url: {TokenID: "a@pam!x", Insecure: true, TLSPinSHA256: pin},
	}}, url
}

func TestResolveInitPin(t *testing.T) {
	oldFP, newFP := strings.Repeat("aa", 32), strings.Repeat("bb", 32)

	t.Run("matching cert keeps stored pin without asking", func(t *testing.T) {
		asked := stubRepin(t, oldFP, nil, true, true)
		cfg, url := repinCfg("SHA256:" + strings.ToUpper(oldFP))
		pin, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, "")
		if err != nil || pveclient.NormalizePin(pin) != oldFP || *asked != 0 {
			t.Fatalf("pin, err, asked = %q, %v, %d", pin, err, *asked)
		}
	})
	t.Run("no stored pin stays TOFU", func(t *testing.T) {
		stubRepin(t, "", errors.New("must not be probed"), true, true)
		cfg, url := repinCfg("")
		if pin, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, ""); pin != "" || err != nil {
			t.Fatalf("pin, err = %q, %v", pin, err)
		}
	})
	t.Run("unfetchable cert keeps enforcing stored pin", func(t *testing.T) {
		stubRepin(t, "", errors.New("network down"), true, true)
		cfg, url := repinCfg(oldFP)
		if pin, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, ""); pin != oldFP || err != nil {
			t.Fatalf("pin, err = %q, %v", pin, err)
		}
	})
	t.Run("changed cert, interactive, confirmed re-pins", func(t *testing.T) {
		asked := stubRepin(t, newFP, nil, true, true)
		cfg, url := repinCfg(oldFP)
		p := &fakePrompter{}
		pin, err := resolveInitPin(context.Background(), p, cfg, url, true, "")
		if err != nil || pin != newFP || *asked != 1 {
			t.Fatalf("pin, err, asked = %q, %v, %d; want new fingerprint", pin, err, *asked)
		}
		if !strings.Contains(p.err.String(), oldFP) || !strings.Contains(p.err.String(), newFP) {
			t.Errorf("both fingerprints must be shown, got %q", p.err.String())
		}
	})
	t.Run("changed cert, interactive, declined aborts", func(t *testing.T) {
		stubRepin(t, newFP, nil, true, false)
		cfg, url := repinCfg(oldFP)
		if _, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, ""); !errors.Is(err, tui.ErrAborted) {
			t.Fatalf("err = %v, want tui.ErrAborted", err)
		}
	})
	t.Run("changed cert already accepted this run is not re-asked", func(t *testing.T) {
		asked := stubRepin(t, newFP, nil, true, false)
		cfg, url := repinCfg(oldFP)
		pin, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, newFP)
		if err != nil || pin != newFP || *asked != 0 {
			t.Fatalf("pin, err, asked = %q, %v, %d", pin, err, *asked)
		}
	})
	t.Run("changed cert, non-interactive fails with how to re-pin", func(t *testing.T) {
		asked := stubRepin(t, newFP, nil, false, true)
		cfg, url := repinCfg(oldFP)
		_, err := resolveInitPin(context.Background(), &fakePrompter{}, cfg, url, true, "")
		if !errors.Is(err, pveclient.ErrTLSVerificationFailed) || *asked != 0 {
			t.Fatalf("err, asked = %v, %d; want ErrTLSVerificationFailed without asking", err, *asked)
		}
		for _, want := range []string{oldFP, newFP, "interactive", "tls_pin_sha256"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error missing %q: %v", want, err)
			}
		}
	})
}

// The linear flow checks a changed certificate before prompting for (and
// so before sending) any credential.
func TestInitLinearChangedCertFailsBeforeCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProbe(t, false, pveclient.ReachTLSUntrusted, pveclient.Reachable)
	stubRepin(t, strings.Repeat("bb", 32), nil, false, true)
	cfg, url := repinCfg(strings.Repeat("aa", 32))
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	p := &fakePrompter{inputs: []string{url, "y"}}
	err := runInteractiveLinear(context.Background(), p)
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Fatalf("err = %v, want ErrTLSVerificationFailed", err)
	}
	if p.idx != 2 || p.sidx != 0 {
		t.Errorf("prompted past the pin check: inputs used %d, secrets used %d", p.idx, p.sidx)
	}
}

// A re-pinned fingerprint is what persistServer stores.
func TestPersistServerSavesRepinnedFingerprint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	newFP := strings.Repeat("bb", 32)
	cfg, url := repinCfg(strings.Repeat("aa", 32))
	err := persistServer(&fakePrompter{}, cfg, persistInput{
		canonical: url, tokenID: "a@pam!x", secret: "s", insecure: true, pin: newFP,
		sshKey: writePubKey(t, "ssh-ed25519 AAAA test@host\n"), user: "ubuntu",
	})
	if err != nil {
		t.Fatalf("persistServer: %v", err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Servers[url].TLSPinSHA256; got != newFP {
		t.Errorf("saved pin = %q, want %q", got, newFP)
	}
}
