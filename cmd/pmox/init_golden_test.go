package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
)

// The golden tests below pin the exact stdout/stderr of the
// non-interactive (linear) init path and the helpers it shares with the
// interactive wizard, so refactoring those helpers into pure cores can't
// silently change what scripted runs print. Re-record with
//
//	go test ./cmd/pmox -run Golden -update
//
// (-update is registered by teatest, which this package's tests import.)
func updateGolden() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// checkGolden compares p's stdout/stderr (with replacements applied, for
// run-specific values like ports and temp dirs) against
// testdata/golden/<name>.golden.
func checkGolden(t *testing.T, name string, p *fakePrompter, replace ...string) {
	t.Helper()
	got := "--- stdout\n" + p.out.String() + "--- stderr\n" + p.err.String()
	if len(replace) > 0 {
		got = strings.NewReplacer(replace...).Replace(got)
	}
	path := filepath.Join("testdata", "golden", name+".golden")
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// goldenPVE serves the handful of read-only endpoints the linear init
// flow touches, over TLS with a self-signed cert so the canonical
// https URL works and the strict→insecure fallback path is exercised.
func goldenPVE(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api2/json")
		body, ok := routes[path]
		if !ok {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGoldenLinearInitHappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProbe(t, false, pveclient.Reachable)
	defer stubSSH(t, func(pvessh.Config) error { return nil }, nil)()

	srv := goldenPVE(t, map[string]string{
		"/version":           `{"data":{"version":"8.2"}}`,
		"/nodes":             `{"data":[{"node":"pve","status":"online"}]}`,
		"/nodes/pve/qemu":    `{"data":[{"vmid":"9000","name":"ubuntu-2404-pmox-9000","template":"1"}]}`,
		"/nodes/pve/storage": `{"data":[{"storage":"local","type":"dir","content":"iso,snippets"},{"storage":"local-lvm","type":"lvmthin","content":"images,rootdir"}]}`,
		"/nodes/pve/network": `{"data":[{"iface":"vmbr0","type":"bridge"}]}`,
	})
	hostPort := strings.TrimPrefix(srv.URL, "https://")
	keyPath := writePubKey(t, "ssh-ed25519 AAAA golden@host\n")

	p := &fakePrompter{
		inputs:  []string{hostPort, "root@pam!pmox", keyPath, "", "", "p"},
		secrets: []string{"tok-secret", "ssh-pass"},
	}
	if err := runInteractiveLinear(context.Background(), p); err != nil {
		t.Fatalf("runInteractiveLinear: %v", err)
	}
	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	checkGolden(t, "linear_happy_path", p, hostPort, "HOST:PORT", strings.ReplaceAll(hostPort, ":", "-"), "HOST-PORT", keyPath, "KEY.pub", cfgDir, "CFG")
}

func TestGoldenProbeOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		statuses []pveclient.ReachStatus
	}{
		{"probe_tls_fallback", []pveclient.ReachStatus{pveclient.ReachTLSUntrusted, pveclient.Reachable}},
		{"probe_tls_untrusted", []pveclient.ReachStatus{pveclient.ReachTLSUntrusted, pveclient.ReachTLSUntrusted}},
		{"probe_not_pve", []pveclient.ReachStatus{pveclient.ReachNotPVE}},
		{"probe_unreachable", []pveclient.ReachStatus{pveclient.ReachUnreachable}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubProbe(t, false, c.statuses...)
			p := &fakePrompter{}
			probeURL(context.Background(), p, "https://pve.home.lan:8006/api2/json")
			checkGolden(t, c.name, p)
		})
	}
}

func TestGoldenRepin(t *testing.T) {
	old, changed := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	for _, c := range []struct {
		name               string
		interactive, trust bool
	}{
		{"repin_accept", true, true},
		{"repin_decline", true, false},
		{"repin_noninteractive", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			stubRepin(t, changed, nil, c.interactive, c.trust)
			cfg, url := repinCfg(old)
			p := &fakePrompter{}
			_, err := resolveInitPin(context.Background(), p, cfg, url, true, "")
			if err != nil {
				p.err.WriteString("error: " + err.Error() + "\n")
			}
			checkGolden(t, c.name, p)
		})
	}
}

func TestGoldenDiscoveryFallbacks(t *testing.T) {
	failing := func() *pveclient.Client {
		srv := goldenPVE(t, map[string]string{
			"/nodes/pve/qemu": `{"data":[{"vmid":"105","name":"web1","template":"0"}]}`,
		})
		return pveclient.NewWithOptions(srv.URL, "root@pam!x", "s", true, pveclient.Options{})
	}
	empty := func() *pveclient.Client {
		srv := goldenPVE(t, map[string]string{
			"/nodes":             `{"data":[]}`,
			"/nodes/pve/qemu":    `{"data":[]}`,
			"/nodes/pve/storage": `{"data":[]}`,
			"/nodes/pve/network": `{"data":[]}`,
		})
		return pveclient.NewWithOptions(srv.URL, "root@pam!x", "s", true, pveclient.Options{})
	}
	stubProbe(t, false, pveclient.Reachable) // pins interactiveFn=false

	type pick func(*fakePrompter, *pveclient.Client) (string, error)
	ctx := context.Background()
	picks := map[string]pick{
		"node":     func(p *fakePrompter, c *pveclient.Client) (string, error) { return pickNode(ctx, p, c, "") },
		"template": func(p *fakePrompter, c *pveclient.Client) (string, error) { return pickTemplate(ctx, p, c, "pve", "") },
		"storage":  func(p *fakePrompter, c *pveclient.Client) (string, error) { return pickStorage(ctx, p, c, "pve", "") },
		"bridge":   func(p *fakePrompter, c *pveclient.Client) (string, error) { return pickBridge(ctx, p, c, "pve", "") },
	}
	for name, fn := range picks {
		for variant, mk := range map[string]func() *pveclient.Client{"error": failing, "empty": empty} {
			t.Run(name+"_"+variant, func(t *testing.T) {
				p := &fakePrompter{inputs: []string{"typed"}}
				got, err := fn(p, mk())
				p.out.WriteString("=> " + got + "\n")
				if err != nil {
					p.err.WriteString("error: " + err.Error() + "\n")
				}
				checkGolden(t, "discovery_"+name+"_"+variant, p, "127.0.0.1", "HOST")
			})
		}
	}
}

func TestGoldenSnippetStorage(t *testing.T) {
	dir := []pveclient.Storage{{Storage: "local", Type: "dir", Content: "iso,vztmpl"}}
	cases := []struct {
		name   string
		client *fakeSnippetClient
		inputs []string
	}{
		{"snippets_list_error", &fakeSnippetClient{listErr: errors.New("403 forbidden")}, nil},
		{"snippets_enable_yes", &fakeSnippetClient{pools: dir}, []string{""}},
		{"snippets_enable_no", &fakeSnippetClient{pools: dir}, []string{"n"}},
		{"snippets_enable_fails", &fakeSnippetClient{pools: dir, updateErr: errors.New("403 forbidden")}, []string{"y"}},
		{"snippets_none_capable", &fakeSnippetClient{pools: []pveclient.Storage{{Storage: "lvm", Type: "lvmthin", Content: "images"}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &fakePrompter{inputs: c.inputs}
			got, err := pickSnippetStorage(context.Background(), p, c.client, "pve", "")
			p.out.WriteString("=> " + got + "\n")
			if err != nil {
				p.err.WriteString("error: " + err.Error() + "\n")
			}
			checkGolden(t, c.name, p)
		})
	}
}

func TestGoldenCloudInitOutcomes(t *testing.T) {
	url := "https://pve.home.lan:8006/api2/json"
	for _, c := range []struct {
		name     string
		existing string
		inputs   []string
	}{
		{"cloudinit_first_write", "", nil},
		{"cloudinit_exists_same_key", "same", nil},
		{"cloudinit_drift_keep", "drift", []string{"n"}},
		{"cloudinit_drift_regen", "drift", []string{"y"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfgDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", cfgDir)
			keyPath := writePubKey(t, "ssh-ed25519 AAAA golden@host\n")
			path, err := config.CloudInitPath(url)
			if err != nil {
				t.Fatal(err)
			}
			switch c.existing {
			case "same":
				writeInitialCloudInit(&fakePrompter{}, url, "ubuntu", keyPath)
			case "drift":
				other := writePubKey(t, "ssh-ed25519 BBBB other@host\n")
				writeInitialCloudInit(&fakePrompter{}, url, "ubuntu", other)
			}
			_ = path
			p := &fakePrompter{inputs: c.inputs}
			writeInitialCloudInit(p, url, "ubuntu", keyPath)
			checkGolden(t, c.name, p, cfgDir, "CFG")
		})
	}
}
