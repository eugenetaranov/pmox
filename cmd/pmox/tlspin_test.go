package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
)

func withStubbedFingerprint(t *testing.T, fp string, err error) {
	t.Helper()
	orig := fetchCertFingerprint
	fetchCertFingerprint = func(context.Context, string) (string, error) { return fp, err }
	t.Cleanup(func() { fetchCertFingerprint = orig })
}

// pinCfg builds a config with one insecure server (optionally pre-pinned)
// under a temp XDG dir so Save writes somewhere disposable.
func pinCfg(t *testing.T, pin string) (*config.Config, *server.Resolved) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://192.168.0.185:8006/api2/json"
	srv := &config.Server{TokenID: "t@pam!x", Insecure: true, TLSPinSHA256: pin}
	cfg := &config.Config{Servers: map[string]*config.Server{url: srv}}
	resolved := &server.Resolved{URL: url, Server: srv, Secret: "s", Source: "test"}
	return cfg, resolved
}

func TestCheckTLSPin_PinsOnFirstConnect(t *testing.T) {
	withStubbedFingerprint(t, "abc123", nil)
	cfg, resolved := pinCfg(t, "")

	var buf bytes.Buffer
	if err := checkTLSPin(context.Background(), &buf, cfg, resolved); err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if resolved.Server.TLSPinSHA256 != "abc123" {
		t.Errorf("pin not set on server, got %q", resolved.Server.TLSPinSHA256)
	}
	if !strings.Contains(buf.String(), "Pinned TLS certificate") {
		t.Errorf("expected a pinned-cert notice, got %q", buf.String())
	}
	// Persisted to disk.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Servers[resolved.URL]; got == nil || got.TLSPinSHA256 != "abc123" {
		t.Errorf("pin not saved to config: %+v", got)
	}
}

func TestCheckTLSPin_MismatchAlerts(t *testing.T) {
	withStubbedFingerprint(t, "newfp999", nil)
	cfg, resolved := pinCfg(t, "oldfp000")

	err := checkTLSPin(context.Background(), &bytes.Buffer{}, cfg, resolved)
	if err == nil {
		t.Fatal("expected an error when the cert changed")
	}
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("err = %v, want ErrTLSVerificationFailed", err)
	}
	if !strings.Contains(err.Error(), "CHANGED") || !strings.Contains(err.Error(), "man-in-the-middle") {
		t.Errorf("alert should be explicit about the change/MITM, got: %v", err)
	}
}

func TestCheckTLSPin_MatchIsSilentSuccess(t *testing.T) {
	withStubbedFingerprint(t, "samefp", nil)
	cfg, resolved := pinCfg(t, "samefp")

	var buf bytes.Buffer
	if err := checkTLSPin(context.Background(), &buf, cfg, resolved); err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a matching pin should print nothing, got %q", buf.String())
	}
}

func TestCheckTLSPin_SecureServerIsNoop(t *testing.T) {
	called := false
	orig := fetchCertFingerprint
	fetchCertFingerprint = func(context.Context, string) (string, error) { called = true; return "x", nil }
	t.Cleanup(func() { fetchCertFingerprint = orig })

	srv := &config.Server{TokenID: "t", Insecure: false}
	resolved := &server.Resolved{URL: "https://pve:8006", Server: srv}
	if err := checkTLSPin(context.Background(), &bytes.Buffer{}, &config.Config{}, resolved); err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if called {
		t.Error("secure servers must not trigger a fingerprint fetch")
	}
}

func TestCheckTLSPin_FetchErrorDoesNotBlock(t *testing.T) {
	withStubbedFingerprint(t, "", errors.New("network down"))
	cfg, resolved := pinCfg(t, "")

	if err := checkTLSPin(context.Background(), &bytes.Buffer{}, cfg, resolved); err != nil {
		t.Fatalf("a fingerprint-fetch error must not block the command: %v", err)
	}
	if resolved.Server.TLSPinSHA256 != "" {
		t.Error("nothing should be pinned when the fetch failed")
	}
}
