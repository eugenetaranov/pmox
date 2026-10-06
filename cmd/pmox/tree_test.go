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
		"vm launch", "vm clone", "vm list", "vm ls", "vm info", "vm start", "vm stop", "vm delete", "vm rm",
		"vm shell", "vm exec", "vm cp", "vm sync", "vm apply", "vm ssh-config",
		"template create", "template list", "template ls",
		"context list", "context ls", "context use", "context current", "context rename", "context delete", "context rm",
		"config edit", "config path", "config cloud-init",
		"mount create", "mount list", "mount ls", "mount delete", "mount rm",
		"key publish", "key unpublish", "key show",
		"access grant", "access revoke", "access list", "access sync",
		"init", "doctor", "cleanup", "version",
	}
	for _, p := range canonical {
		c := findPath(t, p)
		if c.Annotations[deprecatedAnnotation] != "" || c.Hidden {
			t.Errorf("canonical %q is hidden/deprecated", p)
		}
	}
	shortcuts := []string{"launch", "list", "ls", "info", "start", "stop", "delete", "rm", "shell", "exec", "cp", "sync", "apply", "mount", "umount"}
	for _, p := range shortcuts {
		findPath(t, p)
	}
	for _, p := range []string{
		"create-template", "ssh-config", "clone",
		"config get-contexts", "config use-context", "config current-context", "config rename-context", "config delete-context",
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
