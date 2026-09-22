package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/eugenetaranov/pmox/internal/config"
)

func seedTwoServers(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{Servers: map[string]*config.Server{
		"https://a.lan:8006/api2/json": {TokenID: "t@pam!a"},
		"https://b.lan:8006/api2/json": {TokenID: "t@pam!b"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

func newCapturedCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	return c, &out
}

func TestUseContext_SetsAndMaterializesCurrent(t *testing.T) {
	seedTwoServers(t)
	cmd, out := newCapturedCmd()
	// Derived name for the b server is its host "b.lan".
	if err := runUseContext(cmd, "b.lan"); err != nil {
		t.Fatalf("runUseContext: %v", err)
	}
	if !strings.Contains(out.String(), `switched to context "b.lan"`) {
		t.Errorf("unexpected output: %q", out.String())
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CurrentContext != "b.lan" {
		t.Errorf("current_context = %q, want b.lan", reloaded.CurrentContext)
	}
	// use-context materializes the name onto the server.
	if reloaded.Servers["https://b.lan:8006/api2/json"].Name != "b.lan" {
		t.Errorf("name not materialized: %+v", reloaded.Servers["https://b.lan:8006/api2/json"])
	}
}

func TestUseContext_NoArgNonInteractiveErrors(t *testing.T) {
	seedTwoServers(t)
	// Tests don't run under a TTY, so pickContext can't prompt: it must
	// error and list the contexts rather than hang or pick arbitrarily.
	cmd, _ := newCapturedCmd()
	err := runUseContext(cmd, "")
	if err == nil || !strings.Contains(err.Error(), "pass a context name") {
		t.Fatalf("expected a non-interactive error listing contexts, got %v", err)
	}
	if !strings.Contains(err.Error(), "a.lan") || !strings.Contains(err.Error(), "b.lan") {
		t.Errorf("error should list the contexts, got %v", err)
	}
}

func TestUseContext_NoArgSingleContextAutoSelects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{Servers: map[string]*config.Server{
		"https://only.lan:8006/api2/json": {TokenID: "t@pam!x"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cmd, _ := newCapturedCmd()
	if err := runUseContext(cmd, ""); err != nil {
		t.Fatalf("runUseContext: %v", err)
	}
	reloaded, _ := config.Load()
	if reloaded.CurrentContext != "only.lan" {
		t.Errorf("current_context = %q, want only.lan (auto-selected)", reloaded.CurrentContext)
	}
}

func TestUseContext_UnknownErrors(t *testing.T) {
	seedTwoServers(t)
	cmd, _ := newCapturedCmd()
	err := runUseContext(cmd, "nope")
	if err == nil || !strings.Contains(err.Error(), "no context named") {
		t.Fatalf("expected 'no context named' error, got %v", err)
	}
}

func TestGetContexts_MarksCurrent(t *testing.T) {
	seedTwoServers(t)
	orig := outputMode
	outputMode = "text"
	t.Cleanup(func() { outputMode = orig })

	cmd, out := newCapturedCmd()
	if err := runGetContexts(cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No current context") {
		t.Errorf("expected a 'No current context' hint with 2 servers, got %q", out.String())
	}

	cmd2, _ := newCapturedCmd()
	if err := runUseContext(cmd2, "a.lan"); err != nil {
		t.Fatal(err)
	}
	cmd3, out3 := newCapturedCmd()
	if err := runGetContexts(cmd3); err != nil {
		t.Fatal(err)
	}
	// The current context row is marked with * and shows NAME/SERVER.
	if !strings.Contains(out3.String(), "CURRENT") || !strings.Contains(out3.String(), "*") || !strings.Contains(out3.String(), "a.lan") {
		t.Errorf("current context not marked in table: %q", out3.String())
	}
}

func TestRenameContext_UpdatesCurrent(t *testing.T) {
	seedTwoServers(t)
	cmd, _ := newCapturedCmd()
	if err := runUseContext(cmd, "b.lan"); err != nil {
		t.Fatal(err)
	}
	cmd2, out := newCapturedCmd()
	if err := runRenameContext(cmd2, "b.lan", "lab"); err != nil {
		t.Fatalf("runRenameContext: %v", err)
	}
	if !strings.Contains(out.String(), `renamed context "b.lan" to "lab"`) {
		t.Errorf("unexpected output: %q", out.String())
	}
	reloaded, _ := config.Load()
	if reloaded.CurrentContext != "lab" {
		t.Errorf("current should follow rename, got %q", reloaded.CurrentContext)
	}
	if reloaded.Servers["https://b.lan:8006/api2/json"].Name != "lab" {
		t.Errorf("name not renamed: %+v", reloaded.Servers["https://b.lan:8006/api2/json"])
	}
}

func TestRenameContext_DuplicateNameRejected(t *testing.T) {
	seedTwoServers(t)
	cmd, _ := newCapturedCmd()
	err := runRenameContext(cmd, "a.lan", "b.lan")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate-name rejection, got %v", err)
	}
}

func TestCurrentContext_ReportsState(t *testing.T) {
	seedTwoServers(t)
	cmd, out := newCapturedCmd()
	if err := runCurrentContext(cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no current context set (2 configured)") {
		t.Errorf("unexpected current output: %q", out.String())
	}
}

func TestDeleteContext_ClearsCurrent(t *testing.T) {
	keyring.MockInit()
	seedTwoServers(t)
	cmd, _ := newCapturedCmd()
	if err := runUseContext(cmd, "b.lan"); err != nil {
		t.Fatal(err)
	}
	// delete-context resolves the name to a URL, then runRemove drops it.
	if err := runRemove(&testPrompter{}, "https://b.lan:8006/api2/json"); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	reloaded, _ := config.Load()
	if reloaded.CurrentContext != "" {
		t.Errorf("current context should be cleared after deleting it, got %q", reloaded.CurrentContext)
	}
	if _, ok := reloaded.Servers["https://b.lan:8006/api2/json"]; ok {
		t.Error("server b should have been removed")
	}
}

// testPrompter is a no-op prompter for runRemove (it only calls Printf).
type testPrompter struct{}

func (testPrompter) Prompt(string) (string, error)       { return "", nil }
func (testPrompter) PromptSecret(string) (string, error) { return "", nil }
func (testPrompter) Printf(string, ...interface{})       {}
func (testPrompter) Errf(string, ...interface{})         {}
