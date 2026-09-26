package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tackprofile"
)

func TestIsPMOXTemplate(t *testing.T) {
	cases := []struct {
		name string
		vmid int
		want bool
	}{
		{"ubuntu-2404-pmox-9000", 9000, true},
		{"ubuntu-2204-pmox-9050", 9050, true},
		{"ubuntu-2404-pmox-9000", 8000, false}, // out of range
		{"my-own-template", 9000, false},       // no -pmox-
		{"debian-12", 9001, false},
	}
	for _, c := range cases {
		if got := isPMOXTemplate(c.name, c.vmid); got != c.want {
			t.Errorf("isPMOXTemplate(%q,%d) = %v, want %v", c.name, c.vmid, got, c.want)
		}
	}
}

func TestVMOrphanItem(t *testing.T) {
	cmd := &cobra.Command{}
	base := pveclient.Resource{Node: "pve", VMID: 105, Name: "web1", Status: "stopped"}

	cases := []struct {
		name    string
		r       pveclient.Resource
		wantNil bool
	}{
		{"pmox-tagged, no ready tag → flagged", withTags(base, "pmox"), false},
		{"pmox-tagged and ready → not flagged", withTags(base, "pmox;pmox-ready"), true},
		{"not pmox-tagged → not flagged", withTags(base, "other"), true},
		{"no tags at all → not flagged", base, true},
		{"a template, even if tagged pmox → not flagged", func() pveclient.Resource {
			r := withTags(base, "pmox")
			r.Template = 1
			return r
		}(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := vmOrphanItem(context.Background(), cmd, nil, "lab", "https://pve.example:8006/api2/json", c.r)
			if c.wantNil {
				if item != nil {
					t.Errorf("item = %+v, want nil", item)
				}
				return
			}
			if item == nil {
				t.Fatal("item = nil, want a flagged vm item")
			}
			if item.Category != "vm" {
				t.Errorf("Category = %q, want vm", item.Category)
			}
			if !strings.Contains(item.Detail, "web1") || !strings.Contains(item.Detail, "105") {
				t.Errorf("Detail = %q, want it to name the VM", item.Detail)
			}
		})
	}
}

func withTags(r pveclient.Resource, tags string) pveclient.Resource {
	r.Tags = tags
	return r
}

func TestResolveSelection(t *testing.T) {
	avail := []string{"snippet", "template", "log"}

	t.Run("default excludes template", func(t *testing.T) {
		sel, err := resolveSelection(avail, cleanupOpts{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if !sel["snippet"] || !sel["log"] || sel["template"] {
			t.Errorf("default sel = %v; want snippet,log only", sel)
		}
	})
	t.Run("only restricts", func(t *testing.T) {
		sel, _ := resolveSelection(avail, cleanupOpts{only: []string{"snippet"}}, false)
		if !sel["snippet"] || sel["log"] || sel["template"] {
			t.Errorf("only sel = %v", sel)
		}
	})
	t.Run("skip removes", func(t *testing.T) {
		sel, _ := resolveSelection(avail, cleanupOpts{skip: []string{"log"}}, false)
		if !sel["snippet"] || sel["log"] {
			t.Errorf("skip sel = %v", sel)
		}
	})
	t.Run("include-templates adds template", func(t *testing.T) {
		sel, _ := resolveSelection(avail, cleanupOpts{includeTemplates: true}, false)
		if !sel["template"] {
			t.Errorf("include-templates sel = %v", sel)
		}
	})
	t.Run("default excludes vm", func(t *testing.T) {
		sel, _ := resolveSelection([]string{"snippet", "vm"}, cleanupOpts{}, false)
		if sel["vm"] {
			t.Errorf("default sel = %v; want vm excluded", sel)
		}
	})
	t.Run("include-vms adds vm", func(t *testing.T) {
		sel, _ := resolveSelection([]string{"snippet", "vm"}, cleanupOpts{includeVMs: true}, false)
		if !sel["vm"] {
			t.Errorf("include-vms sel = %v", sel)
		}
	})
	t.Run("default excludes context and tack-config", func(t *testing.T) {
		sel, _ := resolveSelection([]string{"snippet", "context", "tack-config"}, cleanupOpts{}, false)
		if sel["context"] || sel["tack-config"] {
			t.Errorf("default sel = %v; want context/tack-config excluded", sel)
		}
	})
	t.Run("generic --include adds any destructive category", func(t *testing.T) {
		sel, _ := resolveSelection([]string{"snippet", "context", "tack-config"}, cleanupOpts{include: []string{"context", "tack-config"}}, false)
		if !sel["context"] || !sel["tack-config"] {
			t.Errorf("--include sel = %v, want both context and tack-config", sel)
		}
	})
	t.Run("generic --include is equivalent to include-templates/include-vms", func(t *testing.T) {
		sel, _ := resolveSelection([]string{"snippet", "template", "vm"}, cleanupOpts{include: []string{"template", "vm"}}, false)
		if !sel["template"] || !sel["vm"] {
			t.Errorf("--include sel = %v, want both template and vm", sel)
		}
	})
	t.Run("unknown key errors", func(t *testing.T) {
		_, err := resolveSelection(avail, cleanupOpts{only: []string{"bogus"}}, false)
		if !errors.Is(err, exitcode.ErrUserInput) {
			t.Errorf("err = %v, want ErrUserInput", err)
		}
	})
	t.Run("unknown --include key errors", func(t *testing.T) {
		_, err := resolveSelection(avail, cleanupOpts{include: []string{"bogus"}}, false)
		if !errors.Is(err, exitcode.ErrUserInput) {
			t.Errorf("err = %v, want ErrUserInput", err)
		}
	})
	t.Run("interactive override via seam", func(t *testing.T) {
		orig := selectCategoriesFn
		selectCategoriesFn = func(_ string, opts []huh.Option[string]) ([]string, error) {
			// template must arrive unchecked, snippet/log checked.
			return []string{"snippet"}, nil // user unchecks log
		}
		t.Cleanup(func() { selectCategoriesFn = orig })
		sel, err := resolveSelection(avail, cleanupOpts{}, true)
		if err != nil {
			t.Fatal(err)
		}
		if !sel["snippet"] || sel["log"] || sel["template"] {
			t.Errorf("interactive sel = %v; want snippet only", sel)
		}
	})
}

func TestCloudInitItems(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	url := "https://a.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "x@y!z"}}}

	dir, _ := config.CloudInitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One file for the configured server (keep), one orphan (flag).
	keepPath, _ := config.CloudInitPath(url)
	if err := os.WriteFile(keepPath, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "gone-host-8006.yaml")
	if err := os.WriteFile(orphan, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	items := cloudInitItems(cfg)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	if items[0].Category != "cloud-init" {
		t.Errorf("category = %q", items[0].Category)
	}
	// Applying removes only the orphan.
	if err := items[0].apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphan not removed")
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Error("configured cloud-init file was removed")
	}
}

func TestTackProfileItems(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	urlA := "https://a.example:8006/api2/json"
	urlGone := "https://gone.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{urlA: {TokenID: "x@y!z"}}}

	_ = tackprofile.Set(testTackStateDir(t), urlA, 101, "web")   // live VM → keep
	_ = tackprofile.Set(testTackStateDir(t), urlA, 555, "old")   // reachable, VM gone → stale
	_ = tackprofile.Set(testTackStateDir(t), urlGone, 1, "orph") // server gone → stale

	vmidsByURL := map[string]map[int]bool{urlA: {101: true}}
	reachable := map[string]bool{urlA: true}

	items := tackProfileItems(cfg, vmidsByURL, reachable)
	if len(items) != 2 {
		t.Fatalf("got %d stale, want 2: %+v", len(items), items)
	}
	for _, it := range items {
		if it.Category != "tack-profile" {
			t.Errorf("category = %q", it.Category)
		}
	}
}

func TestSecretItemsFileBackend(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("PMOX_SECRET_STORE", "file")
	urlA := "https://a.example:8006/api2/json"
	urlGone := "https://gone.example:8006/api2/json"

	if err := credstore.Set(urlA, "secretA"); err != nil {
		t.Fatal(err)
	}
	if err := credstore.Set(urlGone, "secretGone"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Servers: map[string]*config.Server{urlA: {TokenID: "x@y!z"}}}

	items := secretItems(cfg)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	if items[0].Category != "secret" {
		t.Errorf("category = %q", items[0].Category)
	}
	if err := items[0].apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Orphan secret gone, configured secret retained.
	if _, err := credstore.Get(urlGone); !errors.Is(err, credstore.ErrNotFound) {
		t.Error("orphan secret not removed")
	}
	if v, err := credstore.Get(urlA); err != nil || v != "secretA" {
		t.Errorf("configured secret lost: %q %v", v, err)
	}
}

func TestSecretItemsKeychainSkipped(t *testing.T) {
	t.Setenv("PMOX_SECRET_STORE", "keychain")
	cfg := &config.Config{Servers: map[string]*config.Server{}}
	if items := secretItems(cfg); items != nil {
		t.Errorf("keychain backend should yield no secret items, got %+v", items)
	}
}

func TestSplitTokenID(t *testing.T) {
	userid, name, ok := splitTokenID("root@pam!pmox21")
	if !ok || userid != "root@pam" || name != "pmox21" {
		t.Errorf("splitTokenID = %q %q %v", userid, name, ok)
	}
	if _, _, ok := splitTokenID("not-a-token-id"); ok {
		t.Error("expected ok=false for a malformed token id")
	}
}

func TestAPITokenItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/access/users/root@pam/token" {
			t.Errorf("path = %q", r.URL.Path)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"tokenid":"pmox"},
			{"tokenid":"pmox21"},
			{"tokenid":"other-tool"}
		]}`))
	}))
	defer srv.Close()
	client := pveclient.New(srv.URL, "root@pam!pmox21", "secret", false)

	items := apiTokenItems(context.Background(), client, "lab", "root@pam!pmox21")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (only 'pmox', not the configured 'pmox21' or 'other-tool'): %+v", len(items), items)
	}
	if items[0].Category != "api-token" {
		t.Errorf("category = %q", items[0].Category)
	}
}

func TestSSHKeyItems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv := filepath.Join(sshDir, "pmox_ed25519")
	pub := priv + ".pub"
	if err := os.WriteFile(priv, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte("pub"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Referenced by a configured server → not orphaned.
	cfgInUse := &config.Config{Servers: map[string]*config.Server{
		"https://a.example:8006/api2/json": {TokenID: "x@y!z", SSHPubkey: pub},
	}}
	if items := sshKeyItems(cfgInUse); items != nil {
		t.Errorf("key in use should yield no items, got %+v", items)
	}

	// No server references it → orphaned.
	cfgOrphan := &config.Config{Servers: map[string]*config.Server{
		"https://a.example:8006/api2/json": {TokenID: "x@y!z", SSHPubkey: "/some/other/key.pub"},
	}}
	items := sshKeyItems(cfgOrphan)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	if items[0].Category != "ssh-key" {
		t.Errorf("category = %q", items[0].Category)
	}
	if err := items[0].apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(priv); !os.IsNotExist(err) {
		t.Error("private key not removed")
	}
	if _, err := os.Stat(pub); !os.IsNotExist(err) {
		t.Error("public key not removed")
	}
}

func TestContextItems(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("PMOX_SECRET_STORE", "file")
	url := "https://a.example:8006/api2/json"
	cfg := &config.Config{Servers: map[string]*config.Server{url: {TokenID: "x@y!z"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credstore.Set(url, "secret"); err != nil {
		t.Fatal(err)
	}

	items := contextItems(cfg)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	if items[0].Category != "context" {
		t.Errorf("category = %q", items[0].Category)
	}
	if !strings.Contains(items[0].Detail, url) {
		t.Errorf("detail = %q, want it to name the url", items[0].Detail)
	}

	if err := items[0].apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Servers[url]; ok {
		t.Error("context still present in saved config after apply")
	}
	if _, err := credstore.Get(url); err == nil {
		t.Error("secret still present after apply")
	}
}

func TestTackConfigItems(t *testing.T) {
	t.Run("nothing scaffolded yet yields no items", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if items := tackConfigItems(); items != nil {
			t.Errorf("items = %+v, want nil when nothing is scaffolded", items)
		}
	})
	t.Run("removes the whole tack dir", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		dir, err := tackDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "roles", "docker"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "playbook.yaml"), []byte("name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		items := tackConfigItems()
		if len(items) != 1 {
			t.Fatalf("got %d items, want 1: %+v", len(items), items)
		}
		if items[0].Category != "tack-config" {
			t.Errorf("category = %q", items[0].Category)
		}
		if !strings.Contains(items[0].Detail, dir) {
			t.Errorf("detail = %q, want it to name %q", items[0].Detail, dir)
		}
		if err := items[0].apply(); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Error("tack dir not removed")
		}
	})
}
