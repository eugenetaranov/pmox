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

// checkTOFU runs checkTLSPin in the default (pin-and-save) mode.
func checkTOFU(ctx context.Context, w *bytes.Buffer, cfg *config.Config, r *server.Resolved) error {
	_, err := checkTLSPin(ctx, w, cfg, r.URL, r.Server, pinTOFU)
	return err
}

func TestCheckTLSPin_ReturnsPinToEnforce(t *testing.T) {
	withStubbedFingerprint(t, "samefp", nil)
	cfg, resolved := pinCfg(t, "samefp")
	pin, err := checkTLSPin(context.Background(), &bytes.Buffer{}, cfg, resolved.URL, resolved.Server, pinTOFU)
	if err != nil || pin != "samefp" {
		t.Fatalf("pin, err = %q, %v; want samefp, nil", pin, err)
	}

	// A failed probe still enforces the stored pin.
	withStubbedFingerprint(t, "", errors.New("network down"))
	pin, err = checkTLSPin(context.Background(), &bytes.Buffer{}, cfg, resolved.URL, resolved.Server, pinTOFU)
	if err != nil || pin != "samefp" {
		t.Fatalf("probe failure: pin, err = %q, %v; want samefp, nil", pin, err)
	}
}

func TestCheckTLSPin_ReadOnlyNeverSavesOrPrints(t *testing.T) {
	withStubbedFingerprint(t, "fresh", nil)
	cfg, resolved := pinCfg(t, "")
	var buf bytes.Buffer
	pin, err := checkTLSPin(context.Background(), &buf, cfg, resolved.URL, resolved.Server, pinReadOnly)
	if err != nil || pin != "fresh" {
		t.Fatalf("pin, err = %q, %v; want fresh, nil", pin, err)
	}
	if resolved.Server.TLSPinSHA256 != "" {
		t.Errorf("read-only mode stored a pin: %q", resolved.Server.TLSPinSHA256)
	}
	if buf.Len() != 0 {
		t.Errorf("read-only mode printed %q", buf.String())
	}
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
	insecureTLSWarned = false
	t.Cleanup(func() { insecureTLSWarned = false })
	withStubbedFingerprint(t, "abc123", nil)
	cfg, resolved := pinCfg(t, "")

	var buf bytes.Buffer
	if err := checkTOFU(context.Background(), &buf, cfg, resolved); err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if resolved.Server.TLSPinSHA256 != "abc123" {
		t.Errorf("pin not set on server, got %q", resolved.Server.TLSPinSHA256)
	}
	// First connect warns AND pins.
	if !strings.Contains(buf.String(), "WARNING") {
		t.Errorf("first connect should warn that TLS is unverified, got %q", buf.String())
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

// The user-reported bug: after the cert is pinned, neither the WARNING
// nor the "Pinned" line should reappear on subsequent runs.
func TestCheckTLSPin_SilentOnSubsequentRuns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://192.168.0.185:8006/api2/json"
	c := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "t", Insecure: true}}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	withStubbedFingerprint(t, "FP", nil)

	// Run 1: fresh load, first connect — pins and warns.
	insecureTLSWarned = false
	l1, _ := config.Load()
	r1 := &server.Resolved{URL: url, Server: l1.Servers[url]}
	var b1 bytes.Buffer
	if err := checkTOFU(context.Background(), &b1, l1, r1); err != nil {
		t.Fatalf("run1: %v", err)
	}
	if b1.Len() == 0 {
		t.Error("run 1 (first connect) should warn + pin")
	}

	// Run 2: a brand-new process would reset the once-per-process guard.
	insecureTLSWarned = false
	l2, _ := config.Load()
	if l2.Servers[url].TLSPinSHA256 != "FP" {
		t.Fatalf("pin not persisted, got %q", l2.Servers[url].TLSPinSHA256)
	}
	r2 := &server.Resolved{URL: url, Server: l2.Servers[url]}
	var b2 bytes.Buffer
	if err := checkTOFU(context.Background(), &b2, l2, r2); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if b2.Len() != 0 {
		t.Errorf("run 2 (pinned cert matches) must be silent, got: %q", b2.String())
	}
}

func TestCheckTLSPin_MismatchAlerts(t *testing.T) {
	withStubbedFingerprint(t, "newfp999", nil)
	cfg, resolved := pinCfg(t, "oldfp000")

	err := checkTOFU(context.Background(), &bytes.Buffer{}, cfg, resolved)
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
	if err := checkTOFU(context.Background(), &buf, cfg, resolved); err != nil {
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
	if err := checkTOFU(context.Background(), &bytes.Buffer{}, &config.Config{}, resolved); err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if called {
		t.Error("secure servers must not trigger a fingerprint fetch")
	}
}

func TestCheckTLSPin_FetchErrorDoesNotBlock(t *testing.T) {
	withStubbedFingerprint(t, "", errors.New("network down"))
	cfg, resolved := pinCfg(t, "")

	if err := checkTOFU(context.Background(), &bytes.Buffer{}, cfg, resolved); err != nil {
		t.Fatalf("a fingerprint-fetch error must not block the command: %v", err)
	}
	if resolved.Server.TLSPinSHA256 != "" {
		t.Error("nothing should be pinned when the fetch failed")
	}
}

// A pin stored in another accepted spelling (prefix, colons, upper case)
// matches the probed lowercase-hex fingerprint rather than tripping the
// "CHANGED" error.
func TestCheckTLSPin_ComparesNormalizedPins(t *testing.T) {
	withStubbedFingerprint(t, "aabbcc", nil)
	cfg, resolved := pinCfg(t, "SHA256:AA:BB:CC")
	pin, err := checkTLSPin(context.Background(), &bytes.Buffer{}, cfg, resolved.URL, resolved.Server, pinTOFU)
	if err != nil {
		t.Fatalf("checkTLSPin: %v", err)
	}
	if pveclient.NormalizePin(pin) != "aabbcc" {
		t.Errorf("pin = %q, want an equivalent of aabbcc", pin)
	}
}
