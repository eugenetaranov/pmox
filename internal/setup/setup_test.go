package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

const testURL = "https://pve.home.lan:8006/api2/json"

func init() {
	keyring.MockInit()
}

func TestSaveServerStoresSecretsAndClearsStale(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = credstore.SetNodeSSHKeyPassphrase(testURL, "stale")

	cfg := &config.Config{}
	srv := &config.Server{TokenID: "root@pam!pmox", Node: "pve"}
	if err := SaveServer(cfg, testURL, srv, Secrets{Token: "tok", NodeSSHPassword: "pw"}); err != nil {
		t.Fatalf("SaveServer: %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Servers[testURL]; got == nil || got.Node != "pve" {
		t.Fatalf("server not saved: %+v", loaded.Servers)
	}
	if got, _ := credstore.Get(testURL); got != "tok" {
		t.Errorf("token = %q", got)
	}
	if got, _ := credstore.GetNodeSSHPassword(testURL); got != "pw" {
		t.Errorf("node ssh password = %q", got)
	}
	if _, err := credstore.GetNodeSSHKeyPassphrase(testURL); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("stale passphrase not cleared: %v", err)
	}
}

func TestSaveServerRevertsWhenSecretFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	keyring.MockInitWithError(errors.New("keychain locked"))
	t.Cleanup(keyring.MockInit)

	cfg := &config.Config{}
	err := SaveServer(cfg, testURL, &config.Server{TokenID: "root@pam!pmox"}, Secrets{Token: "tok"})
	if err == nil || !strings.Contains(err.Error(), "save secret to keychain") {
		t.Fatalf("err = %v, want save-secret failure", err)
	}
	if _, ok := cfg.Servers[testURL]; ok {
		t.Error("server left in in-memory config after revert")
	}
	loaded, lerr := config.Load()
	if lerr == nil {
		if _, ok := loaded.Servers[testURL]; ok {
			t.Error("server left in saved config after revert")
		}
	}
}

func TestRemoveServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{Servers: map[string]*config.Server{testURL: {TokenID: "x@y!z"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	_ = credstore.Set(testURL, "sekret")

	got, err := RemoveServer("HTTPS://PVE.HOME.LAN/api2/json/")
	if err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	if got != testURL {
		t.Errorf("canonical = %q, want %q", got, testURL)
	}
	loaded, _ := config.Load()
	if _, ok := loaded.Servers[testURL]; ok {
		t.Error("server still in config")
	}
	if _, err := credstore.Get(testURL); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("secret not removed: %v", err)
	}

	if _, err := RemoveServer(testURL); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("second remove: err = %v, want ErrNotFound", err)
	}
}

func TestProbeTLS(t *testing.T) {
	strictErr := errors.New("x509: unknown authority")
	cases := []struct {
		name         string
		strict, insc pveclient.ReachStatus
		wantStatus   pveclient.ReachStatus
		wantInsecure bool
		wantErr      error
	}{
		{"reachable", pveclient.Reachable, pveclient.ReachUnknown, pveclient.Reachable, false, nil},
		{"tls fallback", pveclient.ReachTLSUntrusted, pveclient.Reachable, pveclient.Reachable, true, nil},
		{"tls dead", pveclient.ReachTLSUntrusted, pveclient.ReachUnreachable, pveclient.ReachTLSUntrusted, false, strictErr},
		{"not pve", pveclient.ReachNotPVE, pveclient.ReachUnknown, pveclient.ReachNotPVE, false, strictErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := func(_ context.Context, _ string, insecure bool) (pveclient.ReachStatus, error) {
				if insecure {
					return tc.insc, nil
				}
				if tc.strict == pveclient.Reachable {
					return tc.strict, nil
				}
				return tc.strict, strictErr
			}
			r := ProbeTLS(context.Background(), probe, testURL)
			if r.Status != tc.wantStatus || r.Insecure != tc.wantInsecure || !errors.Is(r.Err, tc.wantErr) {
				t.Errorf("got %+v, want status=%v insecure=%v err=%v", r, tc.wantStatus, tc.wantInsecure, tc.wantErr)
			}
		})
	}
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2"}})
}

func TestVerifyToken(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(versionHandler))
	t.Cleanup(plain.Close)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(versionHandler))
	t.Cleanup(tlsSrv.Close)
	unauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(unauth.Close)
	ctx := context.Background()

	if insecure, err := VerifyToken(ctx, plain.URL, "a@b!c", "s", false, ""); err != nil || insecure {
		t.Errorf("strict: insecure=%v err=%v", insecure, err)
	}
	if insecure, err := VerifyToken(ctx, tlsSrv.URL, "a@b!c", "s", false, ""); err != nil || !insecure {
		t.Errorf("tls fallback: insecure=%v err=%v", insecure, err)
	}
	if insecure, err := VerifyToken(ctx, tlsSrv.URL, "a@b!c", "s", true, ""); err != nil || !insecure {
		t.Errorf("known insecure: insecure=%v err=%v", insecure, err)
	}
	if _, err := VerifyToken(ctx, unauth.URL, "a@b!c", "s", false, ""); !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Errorf("unauthorized: err=%v", err)
	}
}

// TestStoredPinMismatchAbortsBeforeCredentials re-configures a server
// whose certificate was pinned: when the endpoint now presents a
// different certificate, Login and VerifyToken must fail the TLS
// handshake before any request (password or token) reaches it.
func TestStoredPinMismatchAbortsBeforeCredentials(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		versionHandler(w, r)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	stalePin := strings.Repeat("ab", 32)

	if _, err := Login(ctx, srv.URL, true, stalePin, "root@pam", "pw"); !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("Login with stale pin: err = %v, want ErrTLSVerificationFailed", err)
	}
	if _, err := VerifyToken(ctx, srv.URL, "a@b!c", "s", true, stalePin); !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("VerifyToken(knownInsecure) with stale pin: err = %v, want ErrTLSVerificationFailed", err)
	}
	if _, err := VerifyToken(ctx, srv.URL, "a@b!c", "s", false, stalePin); !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		t.Errorf("VerifyToken(fallback) with stale pin: err = %v, want ErrTLSVerificationFailed", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server received %d request(s) despite pin mismatch", n)
	}

	// The matching pin connects normally.
	pin := pveclient.CertFingerprint(srv.Certificate().Raw)
	if insecure, err := VerifyToken(ctx, srv.URL, "a@b!c", "s", true, pin); err != nil || !insecure {
		t.Errorf("matching pin: insecure=%v err=%v", insecure, err)
	}
}

func TestTokenIssuer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/access/ticket":
			_ = r.ParseForm()
			if r.FormValue("password") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"ticket":"PVE:tkt","CSRFPreventionToken":"csrf"}}`))
		case strings.HasPrefix(r.URL.Path, "/access/users/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if name == "taken" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":{"tokenid":"token already exists"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"full-tokenid":"root@pam!` + name + `","value":"secret-` + name + `"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	if _, err := Login(ctx, srv.URL, false, "", "root@pam", ""); !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Fatalf("bad login: err = %v", err)
	}
	iss, err := Login(ctx, srv.URL, false, "", "root@pam", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, _, err := iss.Create(ctx, "taken"); !errors.Is(err, pveclient.ErrTokenExists) {
		t.Errorf("collision: err = %v", err)
	}
	id, secret, err := iss.Create(ctx, "pmox")
	if err != nil || id != "root@pam!pmox" || secret != "secret-pmox" {
		t.Errorf("Create = %q, %q, %v", id, secret, err)
	}
}
