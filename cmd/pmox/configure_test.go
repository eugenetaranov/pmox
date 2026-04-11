package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
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

func TestPromptCanonicalURLRetriesThenSucceeds(t *testing.T) {
	p := &fakePrompter{inputs: []string{"not-a-url", "http://nope", "https://pve.home.lan:8006/api2/json"}}
	got, err := promptCanonicalURL(p)
	if err != nil {
		t.Fatalf("promptCanonicalURL: %v", err)
	}
	if got != "https://pve.home.lan:8006/api2/json" {
		t.Errorf("got %q", got)
	}
}

func TestPromptCanonicalURLExhausted(t *testing.T) {
	p := &fakePrompter{inputs: []string{"bad1", "bad2", "bad3"}}
	_, err := promptCanonicalURL(p)
	if err == nil {
		t.Fatal("want error")
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
	insecure, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret")
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
	insecure, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret")
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
	_, err := validateCredentials(context.Background(), p, srv.URL, "pmox@pve!t", "sekret")
	if !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

func TestOverwritePromptRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://pve.home.lan:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "original@pve!orig"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &fakePrompter{inputs: []string{url, "n"}}
	if err := runInteractive(context.Background(), p); err != nil {
		t.Fatalf("runInteractive: %v", err)
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
