package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExactArgs(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{
			Use:  "x",
			Args: exactArgs(2, "pmox x <a> <b>", "pmox x foo bar"),
			RunE: func(*cobra.Command, []string) error { return nil },
		}
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		return c
	}

	t.Run("too few args gives example-driven error", func(t *testing.T) {
		c := newCmd()
		c.SetArgs([]string{"only-one"})
		err := c.Execute()
		if err == nil || !strings.Contains(err.Error(), "example: pmox x foo bar") {
			t.Fatalf("want example-driven error, got %v", err)
		}
	})

	t.Run("exactly n args passes", func(t *testing.T) {
		c := newCmd()
		c.SetArgs([]string{"a", "b"})
		if err := c.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("post-dash args are not counted", func(t *testing.T) {
		c := newCmd()
		c.SetArgs([]string{"a", "b", "--", "-l", "1000"})
		if err := c.Execute(); err != nil {
			t.Fatalf("post-dash args should not fail validation, got %v", err)
		}
	})
}

func TestSSHInsecure_EnvVarParse(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"yes":   true,
		"no":    false,
		"0":     false,
		"false": false,
	}
	for in, want := range cases {
		t.Setenv("PMOX_SSH_INSECURE", in)
		if got := envBool("PMOX_SSH_INSECURE"); got != want {
			t.Errorf("envBool(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSSHInsecure_WarningOncePerProcess(t *testing.T) {
	origFlag := sshInsecure
	origWarned := sshInsecureWarned
	t.Cleanup(func() {
		sshInsecure = origFlag
		sshInsecureWarned = origWarned
	})
	sshInsecure = true
	sshInsecureWarned = false

	// Capture stderr by redirecting.
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	if !SSHInsecure() {
		t.Fatal("SSHInsecure() = false, want true")
	}
	if !SSHInsecure() {
		t.Fatal("SSHInsecure() second call = false")
	}
	if !SSHInsecure() {
		t.Fatal("SSHInsecure() third call = false")
	}
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	got := buf.String()
	count := strings.Count(got, "WARNING: --ssh-insecure")
	if count != 1 {
		t.Errorf("warning count = %d, want 1 (got: %q)", count, got)
	}
}

func TestSSHInsecure_Disabled(t *testing.T) {
	orig := sshInsecure
	t.Cleanup(func() { sshInsecure = orig })
	sshInsecure = false
	if SSHInsecure() {
		t.Error("SSHInsecure() = true when flag unset")
	}
}

func TestSSHInsecureFlag_Registered(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("ssh-insecure")
	if f == nil {
		t.Fatal("--ssh-insecure flag missing")
	}
}

// tui.Interactive() is false in a test process (no real TTY), so this
// exercises the exact non-interactive path a script/CI hits: a bare
// 'pmox' still just prints help and returns no error, exactly as it did
// before RunE existed on the root command.
func TestRootRunE_NonInteractiveShowsHelp(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "pmox", Short: "test", RunE: rootCmd.RunE}
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("output = %q, want help text", buf.String())
	}
}

func TestRootMenuOptions(t *testing.T) {
	opts := rootMenuOptions(rootCmd)
	if len(opts) == 0 {
		t.Fatal("no menu options")
	}
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Value
	}

	for _, excluded := range []string{"configure", "help", "completion"} {
		if idx := indexOf(names, excluded); idx >= 0 {
			t.Errorf("menu includes %q at %d, want it excluded", excluded, idx)
		}
	}

	for _, n := range []string{"init", "launch", "delete", "apply", "vm", "template", "context", "doctor", "version"} {
		if indexOf(names, n) < 0 {
			t.Fatalf("menu missing %q (names=%v)", n, names)
		}
	}
	for _, hidden := range []string{"create-template", "ssh-config", "clone"} {
		if indexOf(names, hidden) >= 0 {
			t.Errorf("deprecated %q must not be offered (names=%v)", hidden, names)
		}
	}
	// Sections in --help order: Get started, Common, Resources,
	// Maintenance; each alphabetical within (as cobra lists them).
	order := []string{"launch", "delete", "list", "template", "vm", "doctor", "version"}
	for i := 1; i < len(order); i++ {
		if indexOf(names, order[i-1]) >= indexOf(names, order[i]) {
			t.Errorf("%q must come before %q: %v", order[i-1], order[i], names)
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
