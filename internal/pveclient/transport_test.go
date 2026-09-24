package pveclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newPinTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"version":"8.2.4"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPinnedClientMatchingPin(t *testing.T) {
	srv := newPinTestServer(t)
	pin := CertFingerprint(srv.Certificate().Raw)
	for name, p := range map[string]string{
		"plain":           pin,
		"prefixed+upper":  "sha256:" + strings.ToUpper(pin),
		"colon-separated": colonize(pin),
	} {
		t.Run(name, func(t *testing.T) {
			c := NewWithOptions(srv.URL, "t@pam!x", "s", true, Options{PinSHA256: p})
			v, err := c.GetVersion(context.Background())
			if err != nil {
				t.Fatalf("GetVersion with matching pin: %v", err)
			}
			if v != "8.2.4" {
				t.Errorf("version = %q", v)
			}
		})
	}
}

func colonize(hex string) string {
	var parts []string
	for i := 0; i+2 <= len(hex); i += 2 {
		parts = append(parts, hex[i:i+2])
	}
	return strings.Join(parts, ":")
}

func TestPinnedClientWrongPinFails(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)

	c := NewWithOptions(srv.URL, "t@pam!x", "s", true, Options{PinSHA256: strings.Repeat("ab", 32)})
	_, err := c.GetVersion(context.Background())
	if !errors.Is(err, ErrTLSVerificationFailed) {
		t.Fatalf("err = %v, want ErrTLSVerificationFailed", err)
	}
	if errors.Is(err, ErrNetwork) {
		t.Errorf("pin mismatch must not classify as ErrNetwork: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("request reached the server (%d hits) despite pin mismatch", n)
	}
}

func TestNewWithOptionsTimeout(t *testing.T) {
	c := NewWithOptions("https://x", "", "", false, Options{Timeout: 3 * time.Second})
	if c.HTTPClient.Timeout != 3*time.Second {
		t.Errorf("timeout = %v", c.HTTPClient.Timeout)
	}
	if New("https://x", "", "", false).HTTPClient.Timeout != defaultTimeout {
		t.Error("New should use the default timeout")
	}
}

func TestAPIErrorFieldsAndSentinels(t *testing.T) {
	cases := []struct {
		status int
		is     []error
		isNot  []error
	}{
		{http.StatusUnauthorized, []error{ErrUnauthorized}, []error{ErrForbidden, ErrAPIError}},
		{http.StatusForbidden, []error{ErrForbidden, ErrUnauthorized}, []error{ErrAPIError, ErrNotFound}},
		{http.StatusNotFound, []error{ErrNotFound}, []error{ErrAPIError}},
		{http.StatusBadRequest, []error{ErrAPIError}, []error{ErrUnauthorized, ErrNotFound}},
		{http.StatusInternalServerError, []error{ErrAPIError}, []error{ErrTaskFailed}},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"data":null,"message":"boom","errors":{"vmid":"bad"}}`))
			})
			_, err := c.request(context.Background(), "GET", "/x", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err %v is not *APIError", err)
			}
			if apiErr.StatusCode != tc.status || apiErr.Message != "boom" || apiErr.Errors["vmid"] != "bad" {
				t.Errorf("APIError = %+v", apiErr)
			}
			for _, s := range tc.is {
				if !errors.Is(err, s) {
					t.Errorf("want errors.Is(%v)", s)
				}
			}
			for _, s := range tc.isNot {
				if errors.Is(err, s) {
					t.Errorf("unexpected errors.Is(%v)", s)
				}
			}
		})
	}
}

func TestAPIErrorMessageFormat(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":{"b":"y","a":"x"}}`))
	})
	_, err := c.request(context.Background(), "GET", "/x", nil)
	if want := "api error: 400 Bad Request: a: x; b: y"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestResponseBodyIsBounded(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		chunk := []byte(strings.Repeat("x", 1<<20))
		for i := 0; i < (maxResponseBody>>20)+2; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	body, err := c.request(context.Background(), "GET", "/big", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if len(body) != maxResponseBody {
		t.Errorf("body len = %d, want %d", len(body), maxResponseBody)
	}
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	var gotRaw string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if _, err := c.ListStorage(context.Background(), "pve/../x"); err != nil {
		t.Fatalf("ListStorage: %v", err)
	}
	if gotRaw != "/nodes/pve%2F..%2Fx/storage" {
		t.Errorf("escaped path = %q", gotRaw)
	}
}
