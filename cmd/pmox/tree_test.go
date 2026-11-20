package main

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func findPath(t *testing.T, path string) *cobra.Command {
	t.Helper()
	c, rest, err := rootCmd.Find(strings.Fields(path))
	if err != nil || len(rest) != 0 || c == rootCmd {
		t.Fatalf("%q does not resolve (got %v, rest %v, err %v)", path, c.CommandPath(), rest, err)
	}
	return c
}

func TestCommandTreeResolves(t *testing.T) {
	canonical := []string{
		"vm launch", "vm create", "vm clone", "vm list", "vm ls", "vm info", "vm use", "vm start", "vm stop", "vm restart", "vm resize", "vm delete", "vm rm",
		"vm shell", "vm exec", "vm cp", "vm sync", "vm apply", "vm ssh-config",
		"template create", "template list", "template ls",
		"context list", "context ls", "context add", "context use", "context current", "context edit", "context rename", "context delete", "context rm",
		"config path", "config cloud-init",
		"mount create", "mount list", "mount ls", "mount restart", "mount autostart", "mount delete", "mount rm",
		"playbook init", "playbook list", "playbook ls", "playbook default", "playbook update",
		"key list", "key ls", "key publish", "key unpublish", "key show",
		"access setup", "access grant", "access revoke", "access show", "access sync",
		"router create", "router login", "router status", "router delete", "router rm",
		"router route add", "router route list", "router route ls", "router route delete", "router route rm",
		"yardo push", "yardo apply", "yardo verify", "yardo gitkey",
		"init", "doctor", "cleanup", "version",
	}
	for _, p := range canonical {
		c := findPath(t, p)
		if c.Annotations[deprecatedAnnotation] != "" || c.Hidden {
			t.Errorf("canonical %q is hidden/deprecated", p)
		}
	}
	shortcuts := []string{"launch", "create", "list", "ls", "start", "stop", "restart", "resize", "delete", "rm", "shell", "exec", "cp", "sync", "apply", "mount", "umount"}
	// The root's info is the Proxmox host's; a VM's is vm info.
	if c := findPath(t, "info"); c.Annotations[canonicalAnnotation] != "" || !strings.Contains(c.Short, "Proxmox host") {
		t.Errorf("root info = %q (%q), want the host overview", c.Short, c.Annotations[canonicalAnnotation])
	}
	for _, p := range shortcuts {
		findPath(t, p)
	}
	if c := findPath(t, "create"); c.Name() != "launch" {
		t.Errorf("create resolves to %q, want launch", c.Name())
	}
	for _, p := range []string{
		"create-template", "ssh-config", "clone",
		"config get-contexts", "config use-context", "config current-context", "config rename-context", "config delete-context",
		"config edit", "vm resize ram", "vm resize cpu", "vm resize disk",
	} {
		if c := findPath(t, p); !c.Hidden || !strings.HasPrefix(c.Annotations[deprecatedAnnotation], "pmox ") {
			t.Errorf("%q should be a hidden deprecated form pointing at its replacement, got %q", p, c.Annotations[deprecatedAnnotation])
		}
	}
}

// flagSig is a comparable summary of a command's own flags.
func flagSig(c *cobra.Command) []string {
	var sig []string
	c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" { // cobra adds it when a command first runs
			return
		}
		sig = append(sig, f.Name+"/"+f.Shorthand+"="+f.DefValue+":"+f.Value.Type())
	})
	sort.Strings(sig)
	return sig
}

func TestShortcutsMatchCanonicalFlags(t *testing.T) {
	n := 0
	for _, c := range rootCmd.Commands() {
		canon := c.Annotations[canonicalAnnotation]
		if canon == "" {
			continue
		}
		n++
		target := findPath(t, canon)
		if a, b := strings.Join(flagSig(c), " "), strings.Join(flagSig(target), " "); a != b {
			t.Errorf("shortcut %q and %q differ in flags:\n  %s\n  %s", c.Name(), canon, a, b)
		}
		if !strings.Contains(c.Short, "("+canon+")") {
			t.Errorf("shortcut %q summary should name %q: %q", c.Name(), canon, c.Short)
		}
	}
	if n < 12 {
		t.Errorf("only %d shortcuts registered", n)
	}
}

func TestGroupsDoNotShadowRootPreRun(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if s.PersistentPreRun != nil || s.PersistentPreRunE != nil {
				t.Errorf("%s defines a persistent pre-run, shadowing root's input policy", s.CommandPath())
			}
			walk(s)
		}
	}
	walk(rootCmd)
}

func execRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		outputMode, noInput = "text", false
		tui.SetNoInput(false)
	})
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

func TestBareNounWithoutTerminal(t *testing.T) {
	stdout, _, err := execRoot(t, "vm", "--no-input")
	var gh *errGroupHelp
	if !errors.As(err, &gh) || exitcode.From(err) != exitcode.ExitUserError {
		t.Fatalf("err = %v (exit %d), want group help with exit 2", err, exitcode.From(err))
	}
	if !strings.Contains(stdout, "Lifecycle:") || !strings.Contains(stdout, "ssh-config") {
		t.Errorf("help not printed:\n%s", stdout)
	}
}

func TestDeprecatedFormsWriteOnlyToStderr(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox"}}, CurrentContext: "pve.home.lan"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	newOut, newErr, err := execRoot(t, "context", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr, err := execRoot(t, "config", "get-contexts", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if oldOut != newOut {
		t.Errorf("stdout differs:\nold: %s\nnew: %s", oldOut, newOut)
	}
	if strings.Contains(newErr, "deprecated") || !strings.Contains(oldErr, "pmox context list") {
		t.Errorf("stderr: new %q, old %q", newErr, oldErr)
	}
	if !strings.HasPrefix(strings.TrimSpace(oldOut), "{") {
		t.Errorf("deprecation note leaked into stdout: %q", oldOut)
	}
}

func TestInitDeprecatedFlagStillWorks(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "x"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := execRoot(t, "init", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "pmox context list") {
		t.Errorf("stderr = %q, want a pointer to 'pmox context list'", stderr)
	}
}

func TestMountListShowsRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := mount.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "/src/app", RemotePath: "/opt/app", PID: 999999}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execRoot(t, "mount", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web1", "/opt/app", "/src/app", "999999", "stale"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("mount list missing %q:\n%s", want, stdout)
		}
	}
	stdout, _, err = execRoot(t, "mount", "ls", "--output", "json")
	if err != nil || !strings.Contains(stdout, `"vm": "web1"`) || !strings.Contains(stdout, `"running": false`) {
		t.Errorf("json: %v\n%s", err, stdout)
	}
}

func TestMountOldShapeRoutesToMount(t *testing.T) {
	c, rest, err := rootCmd.Find([]string{"mount", "./src", "web1:/opt/app"})
	if err != nil || c.Name() != "mount" || len(rest) != 2 {
		t.Fatalf("got %s %v %v", c.CommandPath(), rest, err)
	}
	if err := c.ValidateArgs(rest); err != nil {
		t.Errorf("old mount shape rejected: %v", err)
	}
}

// Every noun behaves the same when run bare in a script: help + exit 2.
// (On a terminal the same path opens the verb palette.)
func TestEveryNounIsConsistentWhenBare(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.GroupID != groupResources {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			stdout, _, err := execRoot(t, c.Name(), "--no-input")
			var gh *errGroupHelp
			if !errors.As(err, &gh) || exitcode.From(err) != exitcode.ExitUserError {
				t.Fatalf("pmox %s: err = %v (exit %d), want help + exit 2", c.Name(), err, exitcode.From(err))
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("pmox %s printed no help", c.Name())
			}
		})
	}
}

// The command palette runs a verb with no arguments, so every runnable
// command must accept none (and ask, or show its usage, itself).
func TestEveryVerbAcceptsNoArgs(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Runnable() && !c.Hidden && c.Args != nil {
			if err := c.Args(c, nil); err != nil {
				t.Errorf("%s: %v", c.CommandPath(), err)
			}
		}
	}
	walk(rootCmd)
}

// A deprecated flag still works, forwards its value, and says so on
// stderr only.
func TestDeprecatedFlag(t *testing.T) {
	var hard, force bool
	c := &cobra.Command{Use: "stop", RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().BoolVar(&hard, "hard", false, "")
	c.Flags().BoolVar(&force, "force", false, "")
	deprecatedFlag(c, "force", "--hard", "hard")
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)

	c.SetArgs([]string{"--force"})
	if err := c.Execute(); err != nil || !hard {
		t.Fatalf("--force: err %v, hard %v", err, hard)
	}
	if out.Len() != 0 || !strings.Contains(errb.String(), "'--force' is deprecated") || !strings.Contains(errb.String(), "'--hard'") {
		t.Errorf("stdout %q, stderr %q", out.String(), errb.String())
	}
	if !c.Flags().Lookup("force").Hidden || strings.Contains(c.UsageString(), "--force") {
		t.Error("the deprecated flag must not appear in help")
	}

	errb.Reset()
	hard = false
	_ = c.Flags().Set("force", "false")
	c.Flags().Lookup("force").Changed = false
	c.SetArgs([]string{"--hard"})
	if err := c.Execute(); err != nil || !hard || errb.Len() != 0 {
		t.Errorf("--hard: err %v, hard %v, stderr %q", err, hard, errb.String())
	}
}

// --hard always means power-off and --force always means the pmox tag
// check (the router's own route is not a VM).
func TestHardAndForceMeanOneThingEach(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.Flags().Lookup("hard"); f != nil && !strings.Contains(f.Usage, "power-off") {
			t.Errorf("%s --hard: %q should be about powering off", c.CommandPath(), f.Usage)
		}
		if f := c.Flags().Lookup("force"); f != nil && !f.Hidden && !strings.Contains(f.Usage, "tag") && !strings.HasPrefix(c.CommandPath(), "pmox router") {
			t.Errorf("%s --force: %q should be about the pmox tag check", c.CommandPath(), f.Usage)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	for _, p := range []string{"stop", "vm stop", "restart", "delete"} {
		if findPath(t, p).Flags().Lookup("hard") == nil {
			t.Errorf("%s should take --hard", p)
		}
	}
	if f := findPath(t, "stop").Flags().Lookup("force"); f == nil || !f.Hidden {
		t.Error("stop --force should remain as a hidden deprecated flag")
	}
}

func TestServerFlagIsDeprecated(t *testing.T) {
	if f := rootCmd.PersistentFlags().Lookup("server"); f == nil || !f.Hidden {
		t.Fatal("--server should remain as a hidden flag")
	}
	if strings.Contains(rootCmd.UsageString(), "--server") {
		t.Error("--server must not appear in help")
	}
	t.Setenv("PMOX_SERVER", "")
	var b bytes.Buffer
	warnDeprecatedServer(&b)
	if b.Len() != 0 {
		t.Errorf("no note expected: %q", b.String())
	}
	serverFlag = "lab"
	t.Cleanup(func() { serverFlag = "" })
	t.Setenv("PMOX_SERVER", "lab")
	warnDeprecatedServer(&b)
	if !strings.Contains(b.String(), "'--context'") || !strings.Contains(b.String(), "'PMOX_CONTEXT'") {
		t.Errorf("notes = %q", b.String())
	}
}

// ls and rm are the only verb aliases, and always alias list and delete.
func TestOnlyLsAndRmAliases(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, a := range c.Aliases {
			ok := (a == "ls" && c.Name() == "list") || (a == "rm" && c.Name() == "delete") ||
				(c.Name() == "launch" && a == "create") || (c.Name() == "upgrade" && a == "up") ||
				c.CommandPath() == "pmox access show" // also answers to list/ls
			if !ok && !c.Hidden && c.Annotations[deprecatedAnnotation] == "" {
				t.Errorf("%s has alias %q", c.CommandPath(), a)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// remount keeps working, without a note, but root help and the palette
// list only mount and umount.
func TestRemountIsOutOfRootHelp(t *testing.T) {
	c := findPath(t, "remount")
	if !c.Hidden || c.Annotations[deprecatedAnnotation] != "" || c.ValidArgsFunction == nil {
		t.Errorf("remount: hidden %v, deprecated %q; want hidden, not deprecated, still completing", c.Hidden, c.Annotations[deprecatedAnnotation])
	}
	help := rootCmd.UsageString()
	if strings.Contains(help, "remount") || !strings.Contains(help, "umount") || !strings.Contains(help, "  mount ") {
		t.Errorf("root help should list mount and umount, not remount:\n%s", help)
	}
	for _, it := range paletteMenu(rootCmd).Items {
		if it.Key == "remount" {
			t.Error("remount is in the root palette")
		}
	}
}
