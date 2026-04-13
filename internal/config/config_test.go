package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalizeURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"full canonical", "https://pve.home.lan:8006/api2/json", "https://pve.home.lan:8006/api2/json", ""},
		{"trailing slash", "https://pve.home.lan:8006/api2/json/", "https://pve.home.lan:8006/api2/json", ""},
		{"uppercase scheme+host", "HTTPS://PVE.HOME.LAN:8006/api2/json", "https://pve.home.lan:8006/api2/json", ""},
		{"missing port", "https://pve.home.lan/api2/json", "https://pve.home.lan:8006/api2/json", ""},
		{"missing path", "https://pve.home.lan:8006", "https://pve.home.lan:8006/api2/json", ""},
		{"missing port and path", "https://pve.home.lan", "https://pve.home.lan:8006/api2/json", ""},
		{"http rejected", "http://pve.home.lan:8006/api2/json", "", "requires https"},
		{"junk path stripped", "https://pve.home.lan:8006/some/other/path", "https://pve.home.lan:8006/api2/json", ""},
		{"web ui hash stripped", "https://192.168.0.185:8006/#v1:0:18:4:::::::", "https://192.168.0.185:8006/api2/json", ""},
		{"query stripped", "https://pve.home.lan:8006/?foo=bar", "https://pve.home.lan:8006/api2/json", ""},
		{"empty", "", "", "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalizeURL(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{Servers: map[string]*Server{}}
	cfg.AddServer("https://pve.home.lan:8006/api2/json", &Server{
		TokenID:  "pmox@pve!homelab",
		Node:     "pve1",
		Template: "9000",
		Storage:  "local-lvm",
		Bridge:   "vmbr0",
		SSHPubkey:   "~/.ssh/id_ed25519.pub",
		User:     "ubuntu",
		Insecure: true,
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p, _ := Path()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode = %o, want 0700", got)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv, ok := loaded.Servers["https://pve.home.lan:8006/api2/json"]
	if !ok {
		t.Fatal("server missing after roundtrip")
	}
	if srv.TokenID != "pmox@pve!homelab" {
		t.Errorf("TokenID = %q", srv.TokenID)
	}
	if !srv.Insecure {
		t.Errorf("Insecure = false, want true")
	}
}

func TestSnippetStorageRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := &Config{Servers: map[string]*Server{}}
	cfg.AddServer("https://pve.home.lan:8006/api2/json", &Server{
		TokenID:        "x@y!z",
		Storage:        "vm-data",
		SnippetStorage: "local",
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p, _ := Path()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "snippet_storage: local") {
		t.Errorf("yaml missing snippet_storage key:\n%s", raw)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv := loaded.Servers["https://pve.home.lan:8006/api2/json"]
	if srv == nil {
		t.Fatal("server missing after roundtrip")
	}
	if srv.SnippetStorage != "local" {
		t.Errorf("SnippetStorage = %q, want local", srv.SnippetStorage)
	}
	if srv.Storage != "vm-data" {
		t.Errorf("Storage = %q, want vm-data", srv.Storage)
	}
}

func TestSnippetStorageBackCompatMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p, _ := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-split config with no snippet_storage key.
	yaml := "servers:\n" +
		"  https://pve.home.lan:8006/api2/json:\n" +
		"    token_id: x@y!z\n" +
		"    storage: vm-data\n" +
		"    insecure: false\n"
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv := loaded.Servers["https://pve.home.lan:8006/api2/json"]
	if srv == nil {
		t.Fatal("server missing after load")
	}
	if srv.SnippetStorage != "" {
		t.Errorf("SnippetStorage = %q, want empty for back-compat", srv.SnippetStorage)
	}
	if srv.Storage != "vm-data" {
		t.Errorf("Storage = %q, want vm-data", srv.Storage)
	}
}

func TestNodeSSHRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		ns   *NodeSSH
	}{
		{"password-mode", &NodeSSH{User: "root", Auth: "password"}},
		{"key-mode-plain", &NodeSSH{User: "root", Auth: "key", KeyPath: "/home/e/.ssh/pve"}},
		{"key-mode-other-user", &NodeSSH{User: "pmox", Auth: "key", KeyPath: "/key"}},
		{"none", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			cfg := &Config{Servers: map[string]*Server{}}
			cfg.AddServer("https://pve.home.lan:8006/api2/json", &Server{
				TokenID: "t@pam!x", NodeSSH: tc.ns,
			})
			if err := cfg.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			srv := got.Servers["https://pve.home.lan:8006/api2/json"]
			if tc.ns == nil {
				if srv.NodeSSH != nil {
					t.Fatalf("expected nil NodeSSH, got %+v", srv.NodeSSH)
				}
				return
			}
			if srv.NodeSSH == nil {
				t.Fatalf("NodeSSH not persisted")
			}
			if srv.NodeSSH.User != tc.ns.User || srv.NodeSSH.Auth != tc.ns.Auth || srv.NodeSSH.KeyPath != tc.ns.KeyPath {
				t.Fatalf("roundtrip mismatch: got %+v want %+v", srv.NodeSSH, tc.ns)
			}
		})
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("want empty, got %d servers", len(cfg.Servers))
	}
}

func TestServerURLsSorted(t *testing.T) {
	cfg := &Config{Servers: map[string]*Server{
		"https://b.example:8006/api2/json": {},
		"https://a.example:8006/api2/json": {},
	}}
	got := cfg.ServerURLs()
	if got[0] != "https://a.example:8006/api2/json" || got[1] != "https://b.example:8006/api2/json" {
		t.Errorf("unsorted: %v", got)
	}
}

func TestRemoveServer(t *testing.T) {
	cfg := &Config{Servers: map[string]*Server{"x": {}}}
	if !cfg.RemoveServer("x") {
		t.Error("want true for present server")
	}
	if cfg.RemoveServer("x") {
		t.Error("want false for missing server")
	}
}
