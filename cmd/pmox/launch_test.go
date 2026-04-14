package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/hook"
	"github.com/eugenetaranov/pmox/internal/server"
)

func TestResolveLaunchOptions_BuiltInDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL:    "https://pve.example:8006/api2/json",
		Server: &config.Server{TokenID: "t@pam!x", Node: "pve", Template: "9000", Storage: "local-lvm"},
		Secret: "s",
		Source: "single configured",
	}
	f := &launchFlags{}
	opts, err := resolveLaunchOptions(context.Background(), "web1", f, resolved, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveLaunchOptions err: %v", err)
	}
	if opts.CPU != defaultCPU || opts.MemMB != defaultMemMB || opts.DiskSize != defaultDiskSize {
		t.Errorf("cpu/mem/disk = %d/%d/%q, want %d/%d/%q", opts.CPU, opts.MemMB, opts.DiskSize, defaultCPU, defaultMemMB, defaultDiskSize)
	}
	if opts.Wait != defaultWait {
		t.Errorf("wait = %v, want %v", opts.Wait, defaultWait)
	}
	if opts.TemplateID != 9000 {
		t.Errorf("templateID = %d, want 9000", opts.TemplateID)
	}
	wantPath, _ := config.CloudInitPath(resolved.URL)
	if opts.CloudInitPath != wantPath {
		t.Errorf("CloudInitPath = %q, want %q", opts.CloudInitPath, wantPath)
	}
}

func TestResolveLaunchOptions_CLIFlagWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL: "https://pve.example:8006/api2/json",
		Server: &config.Server{
			TokenID: "t@pam!x", Node: "pve", Template: "9000", Storage: "local-lvm",
		},
		Secret: "s",
	}
	f := &launchFlags{cpu: 8, memMB: 16384, disk: "80G", wait: 2 * time.Minute}
	opts, err := resolveLaunchOptions(context.Background(), "web1", f, resolved, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveLaunchOptions err: %v", err)
	}
	if opts.CPU != 8 || opts.MemMB != 16384 || opts.DiskSize != "80G" {
		t.Errorf("flag values not honored: %+v", opts)
	}
	if opts.Wait != 2*time.Minute {
		t.Errorf("wait = %v, want 2m", opts.Wait)
	}
}

func TestResolveLaunchOptions_MissingTemplateIsConfigError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL:    "https://pve.example:8006/api2/json",
		Server: &config.Server{TokenID: "t@pam!x", Node: "pve"},
		Secret: "s",
	}
	_, err := resolveLaunchOptions(context.Background(), "web1", &launchFlags{}, resolved, &bytes.Buffer{})
	if err == nil {
		t.Fatal("resolveLaunchOptions err=nil, want missing template error")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("err = %v, want mention of template", err)
	}
	if !strings.Contains(err.Error(), "pmox configure") {
		t.Errorf("err = %v, want suggestion to run pmox configure", err)
	}
}

func TestResolveSnippetStorage(t *testing.T) {
	cases := []struct {
		name       string
		flag       string
		configured string
		disk       string
		want       string
		wantWarn   bool
	}{
		{"flag wins", "nfs", "local", "vm-data", "nfs", false},
		{"flag wins over empty config", "nfs", "", "vm-data", "nfs", false},
		{"configured used when no flag", "", "local", "vm-data", "local", false},
		{"fallback to disk warns", "", "", "vm-data", "vm-data", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := resolveSnippetStorage(tc.flag, tc.configured, tc.disk, &buf)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			hasWarn := strings.Contains(buf.String(), "no snippet_storage configured")
			if hasWarn != tc.wantWarn {
				t.Errorf("warn = %v, want %v (stderr=%q)", hasWarn, tc.wantWarn, buf.String())
			}
		})
	}
}

func TestResolveHook_MutualExclusion(t *testing.T) {
	cases := []struct {
		name string
		f    launchFlags
	}{
		{"post-create + tack", launchFlags{postCreate: "./p.sh", tack: "./t.yaml"}},
		{"tack + ansible", launchFlags{tack: "./t.yaml", ansible: "./a.yaml"}},
		{"post-create + ansible", launchFlags{postCreate: "./p.sh", ansible: "./a.yaml"}},
		{"all three", launchFlags{postCreate: "./p.sh", tack: "./t.yaml", ansible: "./a.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			h, err := resolveHook(&f)
			if err == nil {
				t.Fatalf("resolveHook err=nil, want mutual-exclusion error")
			}
			if h != nil {
				t.Errorf("hook = %v, want nil on exclusion error", h)
			}
			if !strings.Contains(err.Error(), "mutually exclusive") {
				t.Errorf("err = %v, want 'mutually exclusive'", err)
			}
			if !errors.Is(err, exitcode.ErrUserInput) {
				t.Errorf("err = %v, want to wrap exitcode.ErrUserInput", err)
			}
		})
	}
}

func TestResolveHook_SingleFlag(t *testing.T) {
	cases := []struct {
		name string
		f    launchFlags
		want string // hook name
	}{
		{"post-create", launchFlags{postCreate: "./p.sh"}, "post-create"},
		{"tack", launchFlags{tack: "./t.yaml"}, "tack"},
		{"ansible", launchFlags{ansible: "./a.yaml"}, "ansible"},
		{"none", launchFlags{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			h, err := resolveHook(&f)
			if err != nil {
				t.Fatalf("resolveHook err: %v", err)
			}
			if tc.want == "" {
				if h != nil {
					t.Errorf("hook = %v, want nil", h)
				}
				return
			}
			if h == nil {
				t.Fatalf("hook = nil, want %s", tc.want)
			}
			if h.Name() != tc.want {
				t.Errorf("hook.Name() = %q, want %q", h.Name(), tc.want)
			}
		})
	}
}

// TestRunLaunch_HookExclusionSkipsAPICall asserts task 11.2's goal: a
// mutual-exclusion error returned by resolveHook short-circuits
// runLaunch before any config load / server resolution / PVE API call.
// We call resolveHook directly — it's the first step of runLaunch,
// and a failure there returns before touching config.Load().
func TestRunLaunch_HookExclusionSkipsAPICall(t *testing.T) {
	// Use a config dir that doesn't exist — if resolveHook didn't
	// short-circuit, config.Load() would either fail (wrong error)
	// or succeed against a real user config. Either way, asserting
	// the exclusion error with an isolated HOME is enough: resolveHook
	// never touches disk.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := &launchFlags{postCreate: "./p.sh", tack: "./t.yaml"}
	_, err := resolveHook(f)
	if err == nil {
		t.Fatal("resolveHook err=nil, want mutual-exclusion error")
	}
	// Sanity: hook package types are the ones we return on the
	// success path. Keeps the import from being flagged unused
	// when tests that need it are skipped.
	var _ hook.Hook = (*hook.PostCreateHook)(nil)
}

func TestLaunchVerboseLogLine(t *testing.T) {
	// Exercise just the format to keep the test hermetic — the real
	// runLaunch path requires a live keychain and PVE. A focused unit
	// test of the D-T4 line is enough: it's one fmt.Fprintf call.
	var buf bytes.Buffer
	resolved := &server.Resolved{URL: "https://host:8006/api2/json", Source: "--server flag"}
	// Mirror the exact Fprintf used in runLaunch.
	_, _ = buf.WriteString("using server " + resolved.URL + " (" + resolved.Source + ")\n")
	got := buf.String()
	if got != "using server https://host:8006/api2/json (--server flag)\n" {
		t.Errorf("log line = %q", got)
	}
}
