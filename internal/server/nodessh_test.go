package server

import (
	"path/filepath"
	"testing"
)

func TestResolved_NodeSSHConfig(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	r := &Resolved{
		URL:                  urlA,
		NodeSSHUser:          "root",
		NodeSSHKeyPath:       "/k/id",
		NodeSSHKeyPassphrase: "pp",
	}
	cfg, err := r.NodeSSHConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "pve1.lan:22" || cfg.User != "root" || cfg.KeyPath != "/k/id" || cfg.KeyPass != "pp" || !cfg.Insecure {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if want := filepath.Join(xdg, "pmox", "known_hosts"); cfg.KnownHosts != want {
		t.Errorf("KnownHosts = %q, want %q", cfg.KnownHosts, want)
	}

	if _, err := (&Resolved{URL: "/no/host"}).NodeSSHConfig(false); err == nil {
		t.Error("want error for URL without host")
	}
}
