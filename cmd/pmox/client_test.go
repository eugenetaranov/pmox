package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestWarnInsecureTLS(t *testing.T) {
	t.Run("secure server prints nothing", func(t *testing.T) {
		insecureTLSWarned = false
		var buf bytes.Buffer
		warnInsecureTLS(&buf, "https://host:8006/api2/json", false)
		if buf.Len() != 0 {
			t.Fatalf("expected no output for secure server, got %q", buf.String())
		}
	})

	t.Run("insecure server warns once per process", func(t *testing.T) {
		insecureTLSWarned = false
		var buf bytes.Buffer
		warnInsecureTLS(&buf, "https://host:8006/api2/json", true)
		warnInsecureTLS(&buf, "https://host:8006/api2/json", true)
		if n := strings.Count(buf.String(), "WARNING"); n != 1 {
			t.Fatalf("expected exactly one warning, got %d: %q", n, buf.String())
		}
		if !strings.Contains(buf.String(), "host:8006") {
			t.Fatalf("warning should name the server URL, got %q", buf.String())
		}
	})
}

// wrongPin is a well-formed fingerprint no test certificate will match.
const wrongPin = "0000000000000000000000000000000000000000000000000000000000000000"

// pinnedTLSServer starts a self-signed TLS PVE stand-in that answers
// /version and counts every request that carried an API token.
func pinnedTLSServer(t *testing.T) (srv *httptest.Server, tokenHits *int32) {
	t.Helper()
	var hits int32
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			atomic.AddInt32(&hits, 1)
		}
		_, _ = w.Write([]byte(`{"data":{"version":"8.2.4","release":"8.2","repoid":"x"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// seedInsecureServer writes a hermetic config (one insecure server at
// rawURL with the given pin) plus its secret, and returns the canonical
// URL.
func seedInsecureServer(t *testing.T, rawURL, pin string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PMOX_SECRET_STORE", "file")
	t.Setenv("PMOX_SERVER", "")
	t.Setenv("PMOX_CONTEXT", "")
	url, err := config.CanonicalizeURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Servers: map[string]*config.Server{
		url: {TokenID: "root@pam!pmox", Insecure: true, TLSPinSHA256: pin},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credstore.Set(url, "s3cret"); err != nil {
		t.Fatal(err)
	}
	return url
}

func testCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})
	return cmd, &errBuf
}

func TestConnect_PinMismatchAbortsBeforeTokenSent(t *testing.T) {
	srv, hits := pinnedTLSServer(t)
	seedInsecureServer(t, srv.URL, wrongPin)
	cmd, _ := testCmd()

	_, err := connect(context.Background(), cmd, connectOptions{})
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Fatalf("err = %v, want ErrTLSVerificationFailed", err)
	}
	if !strings.Contains(err.Error(), "CHANGED") {
		t.Errorf("mismatch error should keep the CHANGED/MITM wording, got %v", err)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("server saw %d token-bearing request(s), want 0", n)
	}
}

// The API connection itself must be pinned: even if the probe is fooled
// (or the cert is swapped between probe and request), the request that
// carries the token fails the handshake and never reaches the server.
func TestConnect_APIConnectionIsPinned(t *testing.T) {
	srv, hits := pinnedTLSServer(t)
	seedInsecureServer(t, srv.URL, wrongPin)
	withStubbedFingerprint(t, wrongPin, nil) // probe "matches" the stored pin
	cmd, _ := testCmd()

	s, err := connect(context.Background(), cmd, connectOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = s.Client.GetVersion(context.Background())
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Fatalf("GetVersion err = %v, want ErrTLSVerificationFailed", err)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("server saw %d token-bearing request(s), want 0", n)
	}
}

func TestConnect_FirstConnectPinsAndSaves(t *testing.T) {
	insecureTLSWarned = false
	t.Cleanup(func() { insecureTLSWarned = false })
	origVerbose := verbose
	verbose = true
	t.Cleanup(func() { verbose = origVerbose })

	srv, hits := pinnedTLSServer(t)
	url := seedInsecureServer(t, srv.URL, "")
	cmd, errBuf := testCmd()

	s, err := connect(context.Background(), cmd, connectOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	want := pveclient.CertFingerprint(srv.Certificate().Raw)
	if got := s.Resolved.Server.TLSPinSHA256; got != want {
		t.Errorf("pin = %q, want %q", got, want)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Servers[url].TLSPinSHA256; got != want {
		t.Errorf("saved pin = %q, want %q", got, want)
	}
	if _, err := s.Client.GetVersion(context.Background()); err != nil {
		t.Fatalf("GetVersion on pinned client: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("token-bearing requests = %d, want 1", atomic.LoadInt32(hits))
	}
	stderr := errBuf.String()
	if strings.Count(stderr, "using server "+url) != 1 {
		t.Errorf("want exactly one verbose server line, got %q", stderr)
	}
	if !strings.Contains(stderr, "Pinned TLS certificate") {
		t.Errorf("want pinned notice, got %q", stderr)
	}
}

func TestCleanup_SkipsServerWithPinMismatch(t *testing.T) {
	srv, hits := pinnedTLSServer(t)
	url := seedInsecureServer(t, srv.URL, wrongPin)

	cmd := newCleanupCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !strings.Contains(errBuf.String(), "cleanup: skipping context") || !strings.Contains(errBuf.String(), "CHANGED") {
		t.Errorf("want a skip warning naming the cert change, got %q", errBuf.String())
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("server saw %d token-bearing request(s), want 0", n)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Servers[url].TLSPinSHA256; got != wrongPin {
		t.Errorf("cleanup changed the stored pin to %q", got)
	}
}

func TestCleanup_NeverSavesPins(t *testing.T) {
	srv, _ := pinnedTLSServer(t)
	url := seedInsecureServer(t, srv.URL, "")

	cmd := newCleanupCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Servers[url].TLSPinSHA256; got != "" {
		t.Errorf("cleanup saved a pin (%q); it must be read-only", got)
	}
}
