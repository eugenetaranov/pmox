package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tokenStubServer emulates the PVE ticket + token-create endpoints.
// existingNames are token names that already exist (→ collision error).
func tokenStubServer(t *testing.T, existingNames ...string) *httptest.Server {
	t.Helper()
	exists := map[string]bool{}
	for _, n := range existingNames {
		exists[n] = true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if exists[name] {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":{"tokenid":"token already exists"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"full-tokenid":"root@pam!` + name + `","value":"secret-` + name + `"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGenerateTokenSuccess(t *testing.T) {
	srv := tokenStubServer(t)
	defer srv.Close()

	p := &fakePrompter{inputs: []string{"root@pam", "pmox"}, secrets: []string{"pw"}}
	tokenID, secret, err := generateToken(context.Background(), p, srv.URL, false)
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if tokenID != "root@pam!pmox" {
		t.Errorf("tokenID = %q", tokenID)
	}
	if secret != "secret-pmox" {
		t.Errorf("secret = %q", secret)
	}
	// The password must never surface as the token id or secret.
	if secret == "pw" || tokenID == "pw" {
		t.Error("password leaked into token id/secret")
	}
}

func TestGenerateTokenCollisionReprompts(t *testing.T) {
	srv := tokenStubServer(t, "pmox") // "pmox" taken, "pmox-laptop" free
	defer srv.Close()

	p := &fakePrompter{inputs: []string{"root@pam", "pmox", "pmox-laptop"}, secrets: []string{"pw"}}
	tokenID, secret, err := generateToken(context.Background(), p, srv.URL, false)
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if tokenID != "root@pam!pmox-laptop" || secret != "secret-pmox-laptop" {
		t.Errorf("after collision: tokenID=%q secret=%q", tokenID, secret)
	}
	if !strings.Contains(p.err.String(), "already exists") {
		t.Errorf("expected collision message; stderr=%q", p.err.String())
	}
}

func TestAcquireTokenGenerateRoute(t *testing.T) {
	srv := tokenStubServer(t)
	defer srv.Close()
	origInteractive := interactiveFn
	interactiveFn = func() bool { return true }
	t.Cleanup(func() { interactiveFn = origInteractive })

	// First prompt is the generate/paste choice (blank → generate), then
	// login user + token name; password via secrets.
	p := &fakePrompter{inputs: []string{"", "root@pam", "pmox"}, secrets: []string{"pw"}}
	tokenID, secret, err := acquireToken(context.Background(), p, srv.URL, false)
	if err != nil {
		t.Fatalf("acquireToken: %v", err)
	}
	if tokenID != "root@pam!pmox" || secret != "secret-pmox" {
		t.Errorf("tokenID=%q secret=%q", tokenID, secret)
	}
}

func TestAcquireTokenPasteRoute(t *testing.T) {
	origInteractive := interactiveFn
	interactiveFn = func() bool { return true }
	t.Cleanup(func() { interactiveFn = origInteractive })

	// Choice "paste" → prompt token id then secret.
	p := &fakePrompter{inputs: []string{"paste", "root@pam!existing"}, secrets: []string{"tok-secret"}}
	tokenID, secret, err := acquireToken(context.Background(), p, "https://unused", false)
	if err != nil {
		t.Fatalf("acquireToken: %v", err)
	}
	if tokenID != "root@pam!existing" || secret != "tok-secret" {
		t.Errorf("tokenID=%q secret=%q", tokenID, secret)
	}
}
