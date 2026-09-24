package pvessh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://pve.lan:8006/api2/json": "pve.lan:22",
		"https://192.168.0.185:8006":     "192.168.0.185:22",
	}
	for in, want := range cases {
		got, err := HostFromURL(in)
		if err != nil || got != want {
			t.Errorf("HostFromURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/api2/json", "://bad"} {
		if _, err := HostFromURL(bad); err == nil {
			t.Errorf("HostFromURL(%q): want error", bad)
		}
	}
}

func writeKH(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKnownHostsHas(t *testing.T) {
	path := writeKH(t, "# comment\n\npve.lan ssh-ed25519 AAAA\nother.lan,10.0.0.9 ssh-rsa BBBB\n|1|salt=|hash= ssh-ed25519 CCCC\n")
	cases := map[string]bool{
		"pve.lan:22":   true,
		"pve.lan":      true,
		"10.0.0.9:22":  true,
		"other.lan":    true,
		"missing.lan":  false,
		"pve.lan:2222": false,
		"[pve.lan]:22": false,
	}
	for host, want := range cases {
		got, err := KnownHostsHas(path, host)
		if err != nil {
			t.Fatalf("KnownHostsHas(%q): %v", host, err)
		}
		if got != want {
			t.Errorf("KnownHostsHas(%q) = %v, want %v", host, got, want)
		}
	}

	got, err := KnownHostsHas(filepath.Join(t.TempDir(), "absent"), "pve.lan:22")
	if err != nil || got {
		t.Errorf("missing file: got %v, %v; want false, nil", got, err)
	}

	// Reading a directory is a non-ENOENT error and must surface.
	if _, err := KnownHostsHas(t.TempDir(), "pve.lan:22"); err == nil {
		t.Error("want error reading a directory")
	}
}

func TestKnownHostToken(t *testing.T) {
	cases := map[string]string{
		"192.168.0.60 ssh-ed25519 AAAA":   "192.168.0.60",
		"[192.168.0.60]:22 ssh-rsa BBBB":  "192.168.0.60",
		"[10.0.0.5]:2222 ssh-rsa BBBB":    "10.0.0.5",
		"host.lan,10.0.0.1 ssh-ed25519 C": "host.lan",
		"[fd00::1]:22 ssh-ed25519 D":      "fd00::1",
		"|1|salt=|hash= ssh-ed25519 E":    "|1|salt=|hash=",
		"":                                "",
		"   ":                             "",
	}
	for in, want := range cases {
		if got := KnownHostToken(in); got != want {
			t.Errorf("KnownHostToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKnownHostsPrune(t *testing.T) {
	path := writeKH(t, "# keep me\n10.0.0.1 ssh-ed25519 A\n\n[10.0.0.2]:22 ssh-rsa B\n10.0.0.3 ssh-ed25519 C\n|1|salt=|hash= ssh-ed25519 D\n")
	live := map[string]bool{"10.0.0.1": true, "10.0.0.3": true}
	removed, err := KnownHostsPrune(path, func(h string) bool { return live[h] })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	got, _ := os.ReadFile(path)
	want := "# keep me\n10.0.0.1 ssh-ed25519 A\n10.0.0.3 ssh-ed25519 C\n"
	if string(got) != want {
		t.Errorf("pruned file = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	// Everything pruned → only the comment survives.
	removed, err = KnownHostsPrune(path, func(string) bool { return false })
	if err != nil || removed != 2 {
		t.Fatalf("second prune: removed=%d err=%v", removed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "# keep me\n" {
		t.Errorf("after full prune = %q", got)
	}

	if _, err := KnownHostsPrune(filepath.Join(t.TempDir(), "absent"), func(string) bool { return true }); err == nil {
		t.Error("missing file: want error")
	}
}
