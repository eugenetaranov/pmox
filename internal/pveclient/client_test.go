package pveclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// A bare &http.Transport{} (unlike a browser, or Go's own
// http.DefaultTransport) dials directly and ignores
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY entirely. On a network that routes
// even private-IP traffic through a proxy (common on managed/VPN'd
// machines), that gap alone can make a PVE host the browser reaches
// fine look unreachable to pmox. Compared by function pointer identity
// rather than by setting env vars and observing behavior: Go's
// http.ProxyFromEnvironment memoizes the environment via sync.Once on
// first call, so a t.Setenv-based behavioral test would be flaky
// depending on test execution order within the process.
func TestNewTransport_UsesProxyFromEnvironment(t *testing.T) {
	tr := newTransport(false, "")
	got := reflect.ValueOf(tr.Proxy).Pointer()
	want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
	if got != want {
		t.Error("newTransport's Proxy is not http.ProxyFromEnvironment; won't honor HTTP_PROXY/HTTPS_PROXY/NO_PROXY")
	}
}

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:    srv.URL,
		TokenID:    "pmox@pve!test",
		Secret:     "secret-value",
		HTTPClient: srv.Client(),
	}, srv
}

func TestRequestHappyPath(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "PVEAPIToken=pmox@pve!test=secret-value" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"version":"8.2.4"}}`))
	})
	body, err := c.request(context.Background(), "GET", "/version", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if !strings.Contains(string(body), "8.2.4") {
		t.Errorf("body = %q", body)
	}
}

func TestRequestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		sentin error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrUnauthorized},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusInternalServerError, ErrAPIError},
		{http.StatusBadGateway, ErrAPIError},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			})
			_, err := c.request(context.Background(), "GET", "/whatever", nil)
			if !errors.Is(err, tc.sentin) {
				t.Errorf("want %v, got %v", tc.sentin, err)
			}
		})
	}
}

func TestTLSErrorDetection(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	// Use a client that does NOT trust the test server's cert.
	c := &Client{
		BaseURL:    srv.URL,
		TokenID:    "pmox@pve!test",
		Secret:     "x",
		HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{}}},
	}
	_, err := c.request(context.Background(), "GET", "/", nil)
	if err == nil {
		t.Fatal("want TLS error, got nil")
	}
	if !errors.Is(err, ErrTLSVerificationFailed) {
		t.Errorf("want ErrTLSVerificationFailed, got %v", err)
	}
}

func TestSecretNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Log the full request to the buffer EXCEPT secret should not appear
		// because we only log method+path.
		buf.WriteString(r.Method + " " + r.URL.Path)
		_, _ = w.Write([]byte(`{"data":{"version":"x"}}`))
	})
	if _, err := c.GetVersion(context.Background()); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if strings.Contains(buf.String(), "secret-value") {
		t.Errorf("secret leaked into log buffer: %q", buf.String())
	}
}

func TestGetVersionParsesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/version.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	})
	v, err := c.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v != "8.2.4" {
		t.Errorf("version = %q, want 8.2.4", v)
	}
}

func TestIsTLSError(t *testing.T) {
	if !isTLSError(x509.UnknownAuthorityError{}) {
		t.Error("UnknownAuthorityError not detected")
	}
	if isTLSError(errors.New("random")) {
		t.Error("random error wrongly classified as TLS")
	}
}
