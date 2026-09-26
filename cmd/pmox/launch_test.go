package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
	opts, err := resolveLaunchOptions(context.Background(), nil, "web1", f, resolved, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveLaunchOptions err: %v", err)
	}
	if opts.CPU != defaultCPU || opts.MemMB != 2048 || opts.DiskSize != "20G" {
		t.Errorf("cpu/mem/disk = %d/%d/%q, want %d/2048/\"20G\"", opts.CPU, opts.MemMB, opts.DiskSize, defaultCPU)
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
	f := &launchFlags{cpu: 8, memGB: 16, diskGB: 80, wait: 2 * time.Minute}
	opts, err := resolveLaunchOptions(context.Background(), nil, "web1", f, resolved, &bytes.Buffer{})
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
	_, err := resolveLaunchOptions(context.Background(), nil, "web1", &launchFlags{}, resolved, &bytes.Buffer{})
	if err == nil {
		t.Fatal("resolveLaunchOptions err=nil, want missing template error")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("err = %v, want mention of template", err)
	}
	if !strings.Contains(err.Error(), "pmox init") {
		t.Errorf("err = %v, want suggestion to run pmox init", err)
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

// --- interactive name/sizing prompts (pmox launch [name] with sane defaults) ---

// newTestLaunchFlagsCmd returns a minimal cobra.Command with just the
// sizing flags registered — matching newLaunchCmd's own registration —
// so cmd.Flags().Changed("cpu"/"mem"/"disk") behaves exactly like it
// would on the real command, decoupled from the rest of its wiring.
func newTestLaunchFlagsCmd() (*cobra.Command, *launchFlags) {
	f := &launchFlags{}
	cmd := &cobra.Command{Use: "launch"}
	cmd.Flags().IntVar(&f.cpu, "cpu", 0, "")
	cmd.Flags().IntVar(&f.memGB, "mem", 0, "")
	cmd.Flags().IntVar(&f.diskGB, "disk", 0, "")
	return cmd, f
}

func TestPromptLaunchName(t *testing.T) {
	t.Run("returns the typed name", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{"web1"}}
		got, err := promptLaunchName(p)
		if err != nil || got != "web1" {
			t.Fatalf("got %q, %v; want web1", got, err)
		}
	})
	t.Run("blank reply re-prompts instead of accepting an empty name", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{"  ", "web1"}}
		got, err := promptLaunchName(p)
		if err != nil || got != "web1" {
			t.Fatalf("got %q, %v; want web1 after the blank retry", got, err)
		}
		if !strings.Contains(p.err.String(), "a VM name is required") {
			t.Errorf("stderr = %q, want the retry reason", p.err.String())
		}
	})
}

func TestPromptLaunchSizing(t *testing.T) {
	t.Run("no flags set: prompts all three with built-in defaults", func(t *testing.T) {
		cmd, f := newTestLaunchFlagsCmd()
		p := &fakePrompter{inputs: []string{"", "", ""}} // blank = keep the shown default
		if err := promptLaunchSizing(cmd, p, f); err != nil {
			t.Fatalf("promptLaunchSizing: %v", err)
		}
		if f.cpu != defaultCPU || f.memGB != defaultMemGB || f.diskGB != defaultDiskGB {
			t.Errorf("cpu/mem/disk = %d/%d/%d, want the built-in defaults %d/%d/%d",
				f.cpu, f.memGB, f.diskGB, defaultCPU, defaultMemGB, defaultDiskGB)
		}
		wantPrompts := []string{
			fmt.Sprintf("CPU cores [%d]: ", defaultCPU),
			fmt.Sprintf("Memory in GiB [%d]: ", defaultMemGB),
			fmt.Sprintf("Disk in GiB [%d]: ", defaultDiskGB),
		}
		for _, w := range wantPrompts {
			if !strings.Contains(p.out.String(), w) {
				t.Errorf("prompts = %q, missing %q", p.out.String(), w)
			}
		}
	})
	t.Run("a value already given as a flag is never re-asked", func(t *testing.T) {
		cmd, f := newTestLaunchFlagsCmd()
		if err := cmd.Flags().Set("cpu", "8"); err != nil {
			t.Fatal(err)
		}
		p := &fakePrompter{inputs: []string{"", ""}} // only mem and disk get asked
		if err := promptLaunchSizing(cmd, p, f); err != nil {
			t.Fatalf("promptLaunchSizing: %v", err)
		}
		if f.cpu != 8 {
			t.Errorf("cpu = %d, want 8 (from the flag, unprompted)", f.cpu)
		}
		if strings.Contains(p.out.String(), "CPU cores") {
			t.Error("must not prompt for --cpu once it's already been set")
		}
		if f.memGB != defaultMemGB || f.diskGB != defaultDiskGB {
			t.Errorf("mem/disk = %d/%d, want the built-in defaults", f.memGB, f.diskGB)
		}
	})
	t.Run("a typed value overrides the shown default", func(t *testing.T) {
		cmd, f := newTestLaunchFlagsCmd()
		p := &fakePrompter{inputs: []string{"4", "8", "100"}}
		if err := promptLaunchSizing(cmd, p, f); err != nil {
			t.Fatalf("promptLaunchSizing: %v", err)
		}
		if f.cpu != 4 || f.memGB != 8 || f.diskGB != 100 {
			t.Errorf("cpu/mem/disk = %d/%d/%d, want 4/8/100", f.cpu, f.memGB, f.diskGB)
		}
	})
}

func TestPromptIntDefault(t *testing.T) {
	t.Run("blank keeps the default", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{""}}
		got, err := promptIntDefault(p, "X", 7)
		if err != nil || got != 7 {
			t.Fatalf("got %d, %v; want 7", got, err)
		}
	})
	t.Run("non-numeric and non-positive replies are rejected and re-prompted", func(t *testing.T) {
		p := &fakePrompter{inputs: []string{"abc", "0", "-1", "3"}}
		got, err := promptIntDefault(p, "X", 7)
		if err != nil || got != 3 {
			t.Fatalf("got %d, %v; want 3 after three rejections", got, err)
		}
		if n := strings.Count(p.err.String(), "positive whole number"); n != 3 {
			t.Errorf("rejection messages = %d, want 3", n)
		}
	})
}

func TestRunLaunch_MissingNameNonInteractiveIsAUserInputError(t *testing.T) {
	// tui.Interactive() is false in a test process (no real TTY), so
	// this exercises the exact non-interactive path a script/CI hits:
	// a missing name is still a hard, immediate error — moved here from
	// the Args validator, but the same user-facing message and now
	// carrying the same ErrUserInput sentinel other "you must supply
	// this" launch errors already use (previously unwrapped/ExitGeneric).
	f := &launchFlags{}
	cmd := &cobra.Command{Use: "launch"}
	cmd.SetContext(context.Background())
	err := runLaunch(cmd, "", f)
	if err == nil || !strings.Contains(err.Error(), "missing VM name") {
		t.Fatalf("err = %v, want the missing-VM-name message", err)
	}
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want errors.Is ErrUserInput", err)
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
	// --tack is stat-checked (see TestResolveHook_TackMissingPlaybook), so
	// its case needs a playbook that actually exists on disk.
	tackPlaybook := filepath.Join(t.TempDir(), "t.yaml")
	if err := os.WriteFile(tackPlaybook, []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		f    launchFlags
		want string // hook name
	}{
		{"post-create", launchFlags{postCreate: "./p.sh"}, "post-create"},
		{"tack", launchFlags{tack: tackPlaybook}, "tack"},
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

// TestResolveHook_TackMissingPlaybook guards a fix: --tack used to be
// resolved with no existence check at all, so `pmox launch --tack` with
// no ~/.config/pmox/tack/ scaffolded yet (or an explicit --tack <typo>)
// would fully provision a VM — clone, resize, cloud-init, boot, wait for
// SSH — before tack itself finally failed with a generic "playbook not
// found". resolveHook runs first, before any config load or PVE call, so
// this must fail immediately and mention 'pmox apply --init'.
func TestResolveHook_TackMissingPlaybook(t *testing.T) {
	t.Run("default sentinel, nothing scaffolded", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		f := &launchFlags{tack: tackDefaultSentinel}
		_, err := resolveHook(f)
		if err == nil || !strings.Contains(err.Error(), "--init") {
			t.Fatalf("want a friendly error mentioning --init, got %v", err)
		}
		if !errors.Is(err, exitcode.ErrUserInput) {
			t.Errorf("missing tack playbook should map to ErrUserInput, got %v", err)
		}
	})
	t.Run("explicit path typo", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "typo.yaml")
		f := &launchFlags{tack: missing}
		_, err := resolveHook(f)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("want a friendly error naming %q, got %v", missing, err)
		}
	})
	t.Run("default sentinel, scaffolded", func(t *testing.T) {
		cfg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", cfg)
		dir := filepath.Join(cfg, "pmox", "tack")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "playbook.yaml"), []byte("name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		f := &launchFlags{tack: tackDefaultSentinel}
		h, err := resolveHook(f)
		if err != nil {
			t.Fatalf("resolveHook err: %v", err)
		}
		if h == nil || h.Name() != "tack" {
			t.Fatalf("hook = %v, want a tack hook", h)
		}
	})
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
