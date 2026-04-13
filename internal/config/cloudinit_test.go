package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/snippet"
)

func TestSlug(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"canonical with port and path", "https://192.168.0.185:8006/api2/json", "192.168.0.185-8006", false},
		{"canonical with port, no path", "https://pve.example.com:8006", "pve.example.com-8006", false},
		{"no port uses default", "https://pve.example.com", "pve.example.com-8006", false},
		{"port 443", "https://pve.example.com:443", "pve.example.com-443", false},
		{"malformed missing host", "https://", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Slug(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Slug(%q) err=nil, want err", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Slug(%q) err: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCloudInitPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got, err := CloudInitPath("https://192.168.0.185:8006/api2/json")
	if err != nil {
		t.Fatal(err)
	}
	want := "/tmp/xdg-test/pmox/cloud-init/192.168.0.185-8006.yaml"
	if got != want {
		t.Errorf("CloudInitPath = %q, want %q", got, want)
	}
}

func TestRenderTemplate(t *testing.T) {
	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@host"
	out, err := RenderTemplate("ubuntu", pubkey)
	if err != nil {
		t.Fatal(err)
	}
	if err := snippet.ValidateContent(out); err != nil {
		t.Errorf("rendered template failed ValidateContent: %v", err)
	}
	if !bytes.Contains(out, []byte(pubkey)) {
		t.Errorf("rendered template missing pubkey: %s", out)
	}
	if !bytes.Contains(out, []byte("ssh_authorized_keys:")) {
		t.Errorf("rendered template missing ssh_authorized_keys: %s", out)
	}
	if !bytes.Contains(out, []byte("name: ubuntu")) {
		t.Errorf("rendered template missing user block: %s", out)
	}
	if !bytes.Contains(out, []byte("qemu-guest-agent")) {
		t.Errorf("rendered template missing qemu-guest-agent: %s", out)
	}
}

func TestWriteStarterCloudInit_FirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "cloud-init.yaml")
	if err := WriteStarterCloudInit(path, "ubuntu", "ssh-ed25519 AAAA test"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
	parentInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := parentInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir mode = %o, want 0700", mode)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("ssh-ed25519 AAAA test")) {
		t.Errorf("content missing pubkey: %s", content)
	}
}

func TestWriteStarterCloudInit_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cloud-init.yaml")
	original := []byte("# my custom cloud-init\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteStarterCloudInit(path, "ubuntu", "ssh-ed25519 AAAA test")
	if !errors.Is(err, ErrCloudInitExists) {
		t.Fatalf("err = %v, want ErrCloudInitExists", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("file was modified: got %q, want %q", got, original)
	}
}

func TestWriteCloudInit_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cloud-init.yaml")
	if err := os.WriteFile(path, []byte("old content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteCloudInit(path, "ubuntu", "ssh-ed25519 NEW test"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("ssh-ed25519 NEW test")) {
		t.Errorf("content not overwritten: %s", got)
	}
	if bytes.Contains(got, []byte("old content")) {
		t.Errorf("old content still present: %s", got)
	}
}

// TestTemplateMatchesExample guards against drift between the embedded
// template and the shipped example file. A literal byte compare is too
// brittle (the example may evolve ahead of the template), but the
// structural points that users depend on must match: both must pass
// ValidateContent, both must contain the same list of required stanzas.
func TestTemplateMatchesExample(t *testing.T) {
	example, err := os.ReadFile("../../examples/cloud-init.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderTemplate("ubuntu", "ssh-ed25519 AAAA... replace-with-your-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := snippet.ValidateContent(example); err != nil {
		t.Errorf("example failed ValidateContent: %v", err)
	}
	if err := snippet.ValidateContent(rendered); err != nil {
		t.Errorf("rendered failed ValidateContent: %v", err)
	}
	required := []string{
		"#cloud-config",
		"users:",
		"ssh_authorized_keys:",
		"package_update: true",
		"qemu-guest-agent",
		"runcmd:",
	}
	for _, stanza := range required {
		if !strings.Contains(string(example), stanza) {
			t.Errorf("example missing stanza %q", stanza)
		}
		if !strings.Contains(string(rendered), stanza) {
			t.Errorf("rendered missing stanza %q", stanza)
		}
	}
}
