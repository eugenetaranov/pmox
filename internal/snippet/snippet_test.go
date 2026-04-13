package snippet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestValidateContent(t *testing.T) {
	big := bytes.Repeat([]byte("a"), MaxBytes+1)
	tests := []struct {
		name    string
		content []byte
		wantErr string
	}{
		{"empty", nil, "empty"},
		{"oversized", big, "max 64 KiB"},
		{"binary", []byte{0xff, 0xfe, 0xfd}, "not valid UTF-8"},
		{"good", []byte("#cloud-config\nhostname: x\n"), ""},
		{"with ssh keys", []byte("#cloud-config\nssh_authorized_keys:\n  - ssh-ed25519 AAA\n"), ""},
		{"without ssh keys", []byte("#cloud-config\nhostname: x\n"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateContent(tc.content)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected err: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestHasSSHKeys(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"top-level", []byte("#cloud-config\nssh_authorized_keys:\n  - ssh-ed25519 AAA\n"), true},
		{"nested under users", []byte("users:\n  - name: foo\n    ssh_authorized_keys:\n      - x\n"), true},
		{"absent", []byte("#cloud-config\nhostname: x\n"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasSSHKeys(tc.content); got != tc.want {
				t.Errorf("HasSSHKeys = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilename(t *testing.T) {
	if got := Filename(104); got != "pmox-104-user-data.yaml" {
		t.Errorf("Filename(104) = %q", got)
	}
}

// --- ValidateStorage ---

func storageClient(t *testing.T, handler http.HandlerFunc) *pveclient.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := pveclient.New(srv.URL, "t@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return c
}

func TestValidateStorage_HappyPath(t *testing.T) {
	c := storageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"storage":"local","type":"dir","content":"iso,snippets,vztmpl","active":1,"enabled":1}]}`)
	})
	if err := ValidateStorage(context.Background(), c, "pve1", "local"); err != nil {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestValidateStorage_Missing(t *testing.T) {
	c := storageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"storage":"local","type":"dir","content":"iso,vztmpl,rootdir,images","active":1,"enabled":1}]}`)
	})
	err := ValidateStorage(context.Background(), c, "pve1", "local")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		`"local"`, "iso,vztmpl,rootdir,images", "/etc/pve/storage.cfg",
		"--storage", "https://pve.proxmox.com/wiki/Storage", "snippets",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("err missing %q: %v", want, err)
		}
	}
}

func TestValidateStorage_UnknownStorage(t *testing.T) {
	c := storageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"storage":"other","type":"dir","content":"snippets","active":1,"enabled":1}]}`)
	})
	err := ValidateStorage(context.Background(), c, "pve1", "local")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found", err)
	}
}

// --- ParseCicustom ---

func TestParseCicustom(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantStorage string
		wantFile    string
		wantErr     bool
	}{
		{"valid", "user=local:snippets/pmox-104-user-data.yaml", "local", "pmox-104-user-data.yaml", false},
		{"valid with meta", "user=local:snippets/pmox-104-user-data.yaml,meta=local:snippets/x.yaml", "local", "pmox-104-user-data.yaml", false},
		{"valid with network first", "network=local:snippets/n.yaml,user=local:snippets/pmox-104-user-data.yaml", "local", "pmox-104-user-data.yaml", false},
		{"no user=", "meta=local:snippets/x.yaml", "", "", true},
		{"missing colon", "user=localpmox-104-user-data.yaml", "", "", true},
		{"missing snippets prefix", "user=local:pmox-104-user-data.yaml", "", "", true},
		{"empty filename", "user=local:snippets/", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, f, err := ParseCicustom(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got storage=%q file=%q", s, f)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if s != tc.wantStorage || f != tc.wantFile {
				t.Errorf("got storage=%q file=%q, want %q %q", s, f, tc.wantStorage, tc.wantFile)
			}
		})
	}
}

// --- Cleanup ---

func TestCleanup_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	c := storageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":null}`)
	})
	err := Cleanup(context.Background(), c, "pve1", "user=local:snippets/pmox-104-user-data.yaml")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/nodes/pve1/storage/local/content/local:snippets/pmox-104-user-data.yaml" {
		t.Errorf("path = %q", gotPath)
	}
}

// TestCleanup_RoutesToCicustomStorage locks in the split-snippet-storage
// invariant: when the disk storage and snippet storage differ, delete
// must read the storage out of the cicustom value and not from any
// server.Storage field. This test exists so a future refactor that
// passes server.Storage into Cleanup would fail loudly here.
func TestCleanup_RoutesToCicustomStorage(t *testing.T) {
	var gotPath string
	c := storageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":null}`)
	})
	// VM disk lives on vm-data; snippet on local. Cleanup must hit local.
	err := Cleanup(context.Background(), c, "pve1", "user=local:snippets/pmox-104-user-data.yaml")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !strings.Contains(gotPath, "/storage/local/") {
		t.Errorf("DELETE routed to %q, want path under /storage/local/", gotPath)
	}
	if strings.Contains(gotPath, "/storage/vm-data/") {
		t.Errorf("DELETE must not touch vm-data: %q", gotPath)
	}
}

func TestCleanup_NotFoundSwallowed(t *testing.T) {
	c := storageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := Cleanup(context.Background(), c, "pve1", "user=local:snippets/pmox-104-user-data.yaml")
	if err != nil {
		t.Errorf("NotFound should be swallowed, got %v", err)
	}
}

func TestCleanup_OtherErrorReturned(t *testing.T) {
	c := storageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	err := Cleanup(context.Background(), c, "pve1", "user=local:snippets/pmox-104-user-data.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, pveclient.ErrNotFound) {
		t.Errorf("err should not be ErrNotFound, got %v", err)
	}
}

func TestCleanup_ParseErrorReturned(t *testing.T) {
	// No request should be issued — use a handler that fails if called.
	c := storageClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be issued for malformed cicustom")
	})
	err := Cleanup(context.Background(), c, "pve1", "meta=local:snippets/x.yaml")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// --- Example file ---

func TestExampleFileIsValid(t *testing.T) {
	content, err := os.ReadFile("../../examples/cloud-init.yaml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	if err := ValidateContent(content); err != nil {
		t.Errorf("ValidateContent: %v", err)
	}
	if !HasSSHKeys(content) {
		t.Error("example must include ssh_authorized_keys")
	}
	if !bytes.Contains(content, []byte("qemu-guest-agent")) {
		t.Error("example must include qemu-guest-agent")
	}
}
