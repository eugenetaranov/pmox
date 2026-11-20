package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func TestOutputShorthand(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	long, _, err := execRoot(t, "context", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	short, _, err := execRoot(t, "context", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if short != long || !strings.HasPrefix(strings.TrimSpace(short), "{") {
		t.Errorf("-o json:\n%s\n--output json:\n%s", short, long)
	}
}

func TestErrorLineDropsTheCategory(t *testing.T) {
	cases := map[string]error{
		"missing VM name — usage: pmox launch <name>": fmt.Errorf("%w: missing VM name — usage: pmox launch <name>", exitcode.ErrUserInput),
		"resolve: bad value":                          fmt.Errorf("resolve: %w: bad value", exitcode.ErrUserInput),
		"resolve":                                     fmt.Errorf("resolve: %w", exitcode.ErrUserInput),
		"user input error":                            exitcode.ErrUserInput,
		"boom":                                        errors.New("boom"),
	}
	for want, err := range cases {
		if got := errorLine(err); got != want {
			t.Errorf("errorLine(%q) = %q, want %q", err, got, want)
		}
	}
}

func TestLaunchWithoutNameIsAUserError(t *testing.T) {
	isolate(t)
	_, _, err := execRoot(t, "launch", "--no-input")
	if exitcode.From(err) != exitcode.ExitUserError {
		t.Fatalf("err = %v (exit %d), want the user-input code", err, exitcode.From(err))
	}
	if got := errorLine(err); !strings.HasPrefix(got, "missing VM name") {
		t.Errorf("error line = %q, want it to start with the message", got)
	}
}

func TestOptionalValueFlagsHideTheirSentinel(t *testing.T) {
	cases := []struct {
		name string
		cmd  *cobra.Command
		flag string
	}{
		{"launch", newLaunchCmd(), "--tack [=path]"},
		{"vm clone", newCloneCmd(), "--tack [=path]"},
		{"apply", newApplyCmd(), "--default [=name]"},
	}
	for _, tc := range cases {
		usage := flagUsages(tc.cmd.Flags())
		if strings.Contains(usage, "pmox-default-playbook") || strings.Contains(usage, "pmox-pick-default") {
			t.Errorf("%s help shows a sentinel:\n%s", tc.name, usage)
		}
		if !strings.Contains(usage, tc.flag) {
			t.Errorf("%s help lacks %q:\n%s", tc.name, tc.flag, usage)
		}
	}
	// Rendering help leaves the flag as it was: it still parses without a value.
	c := cases[0].cmd
	if err := c.Flags().Parse([]string{"--tack"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Flags().Lookup("tack").Value.String(); got != tackDefaultSentinel {
		t.Errorf("--tack without a value = %q, want the default sentinel", got)
	}
	// The help the user sees goes through the same renderer.
	if help := findPath(t, "launch").UsageTemplate(); !strings.Contains(help, "flagUsages .LocalFlags") {
		t.Error("the usage template doesn't use flagUsages")
	}
}

func TestSoleContextIsCurrentAndNamedByHost(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execRoot(t, "context", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "*") || !strings.Contains(stdout, "pve.home.lan") {
		t.Errorf("the only context should be marked current:\n%s", stdout)
	}
	if name := cfg.ContextName(formURL); name != "pve.home.lan" {
		t.Errorf("ContextName = %q", name)
	}

	var out bytes.Buffer
	printHostInfo(&out, &hostInfo{Context: cfg.ContextName(formURL), URL: formURL})
	if !strings.Contains(out.String(), "Context:  pve.home.lan ("+formURL+")") || strings.Contains(out.String(), "unnamed") {
		t.Errorf("info should name the context as 'context list' does:\n%s", out.String())
	}
}

func TestExecVMHint(t *testing.T) {
	nf := fmt.Errorf("VM %q: %w", "uname", pveclient.ErrNotFound)
	got := execVMHint(nf, []string{"uname", "-a"})
	if !errors.Is(got, pveclient.ErrNotFound) || !strings.Contains(got.Error(), "pmox exec -- uname -a") {
		t.Errorf("hint = %v", got)
	}
	if got := execVMHint(nf, []string{"web1", "-A", "--", "ls"}); strings.Contains(got.Error(), "pmox exec --") {
		t.Errorf("no hint when -- was given: %v", got)
	}
	other := errors.New("boom")
	if got := execVMHint(other, []string{"web1", "ls"}); got.Error() != "boom" {
		t.Errorf("no hint for other errors: %v", got)
	}
}

func TestCleanupAPITokensAreOptIn(t *testing.T) {
	items := itemsFromCounts(map[string]int{"api-token": 2, "log": 1})
	avail := []string{"api-token", "log"}

	sel, err := resolveSelection(avail, items, cleanupOpts{apply: true}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if sel["api-token"] || !sel["log"] {
		t.Errorf("--apply selection = %v, want log without api-token", sel)
	}

	sel, err = resolveSelection(avail, items, cleanupOpts{apply: true, include: []string{"api-token"}}, false, io.Discard)
	if err != nil || !sel["api-token"] {
		t.Errorf("--include api-token selection = %v, err %v", sel, err)
	}

	orig := selectCategoriesFn
	t.Cleanup(func() { selectCategoriesFn = orig })
	var label string
	selectCategoriesFn = func(_ string, opts []huh.Option[string]) ([]string, error) {
		var picked []string
		for _, o := range opts {
			if o.Value == "api-token" {
				label = o.Key
			}
			if o.Value == "log" {
				picked = append(picked, o.Value)
			}
		}
		return picked, nil
	}
	sel, err = resolveSelection(avail, items, cleanupOpts{}, true, io.Discard)
	if err != nil || sel["api-token"] {
		t.Errorf("checklist selection = %v, err %v", sel, err)
	}
	if !strings.Contains(label, "(2)") || !strings.Contains(label, "another machine") {
		t.Errorf("api-token row = %q, want its count and the in-use warning", label)
	}
}

func TestCleanupHelpListsEveryCategory(t *testing.T) {
	help := cleanupLong()
	for _, c := range cleanupCategories {
		var line string
		for _, l := range strings.Split(help, "\n") {
			if strings.HasPrefix(l, "  "+c.key+" ") {
				line = l
			}
		}
		if line == "" {
			t.Errorf("--help has no line for category %q", c.key)
			continue
		}
		if c.destructive != strings.HasSuffix(line, "[opt-in]") {
			t.Errorf("category %q: opt-in marker wrong in %q", c.key, line)
		}
	}
	if strings.Contains(help, "four") {
		t.Error("--help states a fixed number of opt-in categories")
	}
}

func TestStalePinsAreCountedIndividually(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := guestKnownHostsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 43; i++ {
		fmt.Fprintf(&b, "192.168.0.%d ssh-ed25519 AAAA%d\n", i, i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	items := staleKnownHostItems(map[string]bool{}, true, io.Discard)
	if len(items) != 1 || countItems(items) != 43 {
		t.Fatalf("items = %d, count = %d, want 1 item covering 43 pins", len(items), countItems(items))
	}
	var out bytes.Buffer
	printItemsByCategory(&out, items)
	got := out.String()
	if !strings.Contains(got, "Stale known_hosts pins (43):") || !strings.Contains(got, "192.168.0.10 and 33 more") || strings.Contains(got, "192.168.0.11,") {
		t.Errorf("report:\n%s", got)
	}
}

func TestMountListStateIsOneWord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := mount.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "/src/app", RemotePath: "/opt/app", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if _, err := mount.Save(dir, mount.Record{VMName: "db1", LocalPath: "/src/db", RemotePath: "/opt/db", PID: 999999}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execRoot(t, "mount", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	var hints int
	for _, l := range lines {
		if strings.HasPrefix(l, "db1") && !strings.HasSuffix(strings.TrimSpace(l), " stale") {
			t.Errorf("stale row should end in the one word 'stale': %q", l)
		}
		if strings.Contains(l, "pmox mount restart --stale") {
			hints++
		}
	}
	if hints != 1 || !strings.Contains(lines[len(lines)-1], "pmox mount delete") {
		t.Errorf("want one hint line after the table:\n%s", stdout)
	}
	js, _, err := execRoot(t, "mount", "list", "-o", "json")
	if err != nil || strings.Contains(js, "restart") {
		t.Errorf("json should carry no hint: %v\n%s", err, js)
	}
}

func TestColorSwitch(t *testing.T) {
	t.Cleanup(func() { tui.SetColor(true) })
	tui.SetColor(false)
	if tui.ColorEnabled() {
		t.Fatal("color should be off")
	}
	if got := colorize("ok", colorGreen); got != "ok" {
		t.Errorf("colorize with color off = %q", got)
	}
}
