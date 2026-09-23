package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
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
	t.Run("unknown key errors", func(t *testing.T) {
		_, err := resolveSelection(avail, cleanupOpts{only: []string{"bogus"}}, false)
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

	_ = tackprofile.Set(tackStateDir(), urlA, 101, "web")   // live VM → keep
	_ = tackprofile.Set(tackStateDir(), urlA, 555, "old")   // reachable, VM gone → stale
	_ = tackprofile.Set(tackStateDir(), urlGone, 1, "orph") // server gone → stale

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
