package pveclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/access/ticket" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		if r.FormValue("username") != "root@pam" || r.FormValue("password") != "s3cret" {
			t.Errorf("bad creds: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"data":{"ticket":"PVE:tkt","CSRFPreventionToken":"csrf123"}}`))
	}))
	defer srv.Close()

	tk, err := Login(context.Background(), srv.URL, false, "root@pam", "s3cret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tk.Cookie != "PVE:tkt" || tk.CSRF != "csrf123" {
		t.Errorf("ticket = %+v", tk)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := Login(context.Background(), srv.URL, false, "root@pam", "wrong")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestCreateTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/access/users/root@pam/token/pmox" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "PVEAuthCookie=PVE:tkt" {
			t.Errorf("cookie = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("CSRFPreventionToken") != "csrf123" {
			t.Errorf("csrf = %q", r.Header.Get("CSRFPreventionToken"))
		}
		_ = r.ParseForm()
		if r.FormValue("privsep") != "0" {
			t.Errorf("privsep = %q, want 0", r.FormValue("privsep"))
		}
		_, _ = w.Write([]byte(`{"data":{"full-tokenid":"root@pam!pmox","value":"abcd-ef01"}}`))
	}))
	defer srv.Close()

	full, secret, err := CreateToken(context.Background(), srv.URL, false, Ticket{Cookie: "PVE:tkt", CSRF: "csrf123"}, "root@pam", "pmox")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if full != "root@pam!pmox" || secret != "abcd-ef01" {
		t.Errorf("full=%q secret=%q", full, secret)
	}
}

func TestCreateTokenAlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"data":null,"errors":{"tokenid":"token already exists"}}`))
	}))
	defer srv.Close()

	_, _, err := CreateToken(context.Background(), srv.URL, false, Ticket{Cookie: "x", CSRF: "y"}, "root@pam", "pmox")
	if !errors.Is(err, ErrTokenExists) {
		t.Errorf("err = %v, want ErrTokenExists", err)
	}
}

func TestCreateTokenEscapesPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"data":{"full-tokenid":"u@pve!n","value":"v"}}`))
	}))
	defer srv.Close()

	_, _, err := CreateToken(context.Background(), srv.URL, false, Ticket{}, "u@pve", "n")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.Contains(gotPath, "token/n") {
		t.Errorf("escaped path = %q", gotPath)
	}
}
