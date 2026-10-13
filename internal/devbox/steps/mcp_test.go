package steps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
)

// EnvSet edits an env file as the bash installer's envset does.
func TestEnvSetMatchesBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	script, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	i := strings.Index(string(script), "\nenvset() {")
	j := strings.Index(string(script)[i+1:], "\n}\n")
	fn := string(script)[i+1 : i+1+j+3]
	for _, tc := range []struct{ in, key, val string }{
		{"", "TOKEN", "abc"},
		{"# A comment\nGITHUB_TOKEN=\n", "GITHUB_TOKEN", "ghp_x"},
		{"#GITHUB_TOKEN=\nOTHER=1\n", "GITHUB_TOKEN", "v"},
		{"# GITHUB_TOKEN=\n", "GITHUB_TOKEN", "v w"},
		{"A=1\nA=2\n", "A", "3"},
		{"B=1", "A", "x"},
	} {
		f := filepath.Join(t.TempDir(), "env")
		if err := os.WriteFile(f, []byte(tc.in), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("bash", "-c", fn+"\nenvset \"$1\" \"$2\" \"$3\"", "_", tc.key, tc.val, f).CombinedOutput(); err != nil {
			t.Fatalf("bash envset: %v\n%s", err, out)
		}
		want, _ := os.ReadFile(f)
		if got := EnvSet([]byte(tc.in), tc.key, tc.val); string(got) != string(want) {
			t.Errorf("EnvSet(%q, %s): %q, bash %q", tc.in, tc.key, got, want)
		}
	}
}

func mcpEnv(t *testing.T, x *fakeExec) *Env {
	t.Helper()
	e := testEnv(t, x)
	e.Share = t.TempDir()
	e.State = t.TempDir()
	put(t, filepath.Join(e.Share, "mcp-catalog", "github.spec"), "NAME=github\nKIND=docker\nskeleton() { echo 'GITHUB_TOKEN='; }\n")
	put(t, filepath.Join(e.Share, "conf", "mcp-catalog", "github.spec"), "NAME=github\nKIND=docker\nskeleton() { printf '# GitHub\\nGITHUB_PERSONAL_ACCESS_TOKEN=\\n'; }\n")
	put(t, filepath.Join(e.Share, "mcp-sync"), "#!/bin/sh\n")
	wire := true
	e.Answers = &answers.Answers{MCPServers: []string{"github"}, WireAgents: &wire}
	e.Secrets = creds.New()
	return e
}

func TestMCPFiles(t *testing.T) {
	e := mcpEnv(t, &fakeExec{})
	st := step(t, catalog.Step{Type: "go:mcp_files"})
	apply(t, e, st)
	// pmox's own spec wins over tack-roles' of the same name.
	if got := read(t, e.Path(mcpCatalog+"/github.spec")); !strings.Contains(got, "PERSONAL_ACCESS") {
		t.Errorf("github.spec = %q", got)
	}
	if fi, _ := os.Stat(e.Path(mcpSync)); fi.Mode().Perm() != 0o755 {
		t.Errorf("mcp-sync mode %v", fi.Mode())
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
}

func TestMCPServersEnv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash (specs are shell)")
	}
	e := mcpEnv(t, &fakeExec{})
	// Only pmox's spec (tack-roles' names never overlap it).
	if err := os.Remove(filepath.Join(e.Share, "mcp-catalog", "github.spec")); err != nil {
		t.Fatal(err)
	}
	st := step(t, catalog.Step{Type: "go:mcp_servers"})
	if check(t, e, st) {
		t.Fatal("Check with no env file = true")
	}
	apply(t, e, st)
	p := e.Path("~/.mcp/github/env")
	if got := read(t, p); got != "# GitHub\nGITHUB_PERSONAL_ACCESS_TOKEN=\n" {
		t.Errorf("skeleton env = %q", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("env mode %v", fi.Mode())
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
	if got := answers.ReadCreated(e.State); len(got) != 1 || got[0] != (answers.Created{Kind: "file", Value: "/home/ada/.mcp/github/env"}) {
		t.Errorf("created = %+v", got)
	}
	// A key typed on a later run goes in; the user's edits stay.
	put(t, p, "# GitHub\nGITHUB_PERSONAL_ACCESS_TOKEN=\nEXTRA=mine\n")
	e.Secrets.MCP["github GITHUB_PERSONAL_ACCESS_TOKEN"] = "ghp_1"
	if check(t, e, st) {
		t.Error("Check with a new key typed = true")
	}
	apply(t, e, st)
	if got := read(t, p); got != "# GitHub\nGITHUB_PERSONAL_ACCESS_TOKEN=ghp_1\nEXTRA=mine\n" {
		t.Errorf("env = %q", got)
	}
	if len(answers.ReadCreated(e.State)) != 1 {
		t.Error("an existing env file was recorded as created")
	}
}

func TestMCPUnit(t *testing.T) {
	active := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "systemctl" && (c.Args[0] == "is-enabled" || c.Args[0] == "is-active") && !active {
			return nil, errors.New("inactive")
		}
		return nil, nil
	}}
	e := mcpEnv(t, x)
	e.Answers.MCPListen, e.Answers.MCPPort = "0.0.0.0", 8081
	// Phase 1's single gateway ran as ada: the instance replaces it.
	put(t, e.Path(oldMCPUnit), "[Service]\nUser=ada\n")
	st := step(t, catalog.Step{Type: "go:mcpjungle_unit"})
	if check(t, e, st) {
		t.Fatal("Check = true before the unit is written")
	}
	apply(t, e, st)
	if got := read(t, e.Path(mcpUnit)); !strings.Contains(got, "User=mcp-%i\n") || !strings.Contains(got, "EnvironmentFile=/etc/mcpjungle/%i.env") {
		t.Errorf("unit:\n%s", got)
	}
	if got := read(t, e.Path("/etc/mcpjungle/ada.env")); got != "MCP_HOST=0.0.0.0\nMCP_PORT=8081\nMCP_DIR=/home/ada/.mcp\n" {
		t.Errorf("env = %q", got)
	}
	if _, err := os.Stat(e.Path(oldMCPUnit)); err == nil {
		t.Error("the old mcpjungle.service is still there")
	}
	if got := strings.Join(x.commands(), "; "); got != "systemctl disable --now mcpjungle; systemctl daemon-reload; systemctl enable mcpjungle@ada; systemctl restart mcpjungle@ada" {
		t.Errorf("ran %s", got)
	}
	active = true
	if !check(t, e, st) {
		t.Error("Check with the unit written and running = false")
	}
	// Unchanged unit, stopped service: only enable --now.
	active = false
	x.ran = nil
	apply(t, e, st)
	if got := strings.Join(x.commands(), "; "); got != "systemctl enable --now mcpjungle@ada" {
		t.Errorf("ran %s", got)
	}
	// Another user's old gateway is left alone.
	put(t, e.Path(oldMCPUnit), "[Service]\nUser=bob\n")
	active = true
	if !check(t, e, st) {
		t.Error("bob's old gateway made ada's step undone")
	}
}

func TestMCPWaitNotes(t *testing.T) {
	e := mcpEnv(t, &fakeExec{})
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	oldUp, oldTries := mcpUp, mcpWaitTries
	defer func() { mcpUp, mcpWaitTries = oldUp, oldTries }()
	mcpUp, mcpWaitTries = func(context.Context, string) bool { return false }, 1
	apply(t, e, step(t, catalog.Step{Type: "go:mcp_wait"}))
	if len(notes) != 1 || !strings.Contains(notes[0], "journalctl -u mcpjungle@ada") {
		t.Errorf("notes = %q", notes)
	}
}

func TestMCPWireCodex(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "sh" && strings.HasSuffix(c.Args[1], " codex") {
			return nil, nil // codex is installed
		}
		return nil, errors.New("no")
	}}
	e := mcpEnv(t, x)
	st := step(t, catalog.Step{Type: "go:mcp_wire"})
	put(t, e.Path("~/.codex/config.toml"), "model = \"o4\"\n")
	if check(t, e, st) {
		t.Fatal("Check = true before wiring codex")
	}
	apply(t, e, st)
	if got := read(t, e.Path("~/.codex/config.toml")); got != "model = \"o4\"\n\n[mcp_servers.mcpjungle]\nurl = \"http://localhost:8080/mcp\"\n" {
		t.Errorf("config.toml = %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check after wiring = false")
	}
	// The user's port moved: the url is rewritten in place.
	e.Answers.MCPPort = 8082
	if check(t, e, st) {
		t.Error("Check = true with the old port")
	}
	apply(t, e, st)
	if got := read(t, e.Path("~/.codex/config.toml")); got != "model = \"o4\"\n\n[mcp_servers.mcpjungle]\nurl = \"http://localhost:8082/mcp\"\n" {
		t.Errorf("config.toml after the move = %q", got)
	}
	no := false
	e.Answers.WireAgents = &no
	if !check(t, e, st) {
		t.Error("not wiring: Check = false")
	}
}

func TestMCPSyncAlways(t *testing.T) {
	x := &fakeExec{}
	e := mcpEnv(t, x)
	st := step(t, catalog.Step{Type: "go:mcp_sync"})
	if a, ok := st.(Always); !ok || !a.Always() {
		t.Fatal("mcp_sync is not an Always step")
	}
	apply(t, e, st)
	if got := x.commands(); len(got) != 1 || got[0] != "(as user) env MCP_DIR=/home/ada/.mcp MCP_CATALOG=/usr/local/share/mcp-catalog MCPJUNGLE_REGISTRY=http://127.0.0.1:8080 /usr/local/bin/mcp-sync github" {
		t.Errorf("ran %q", got)
	}
}

// As root, ~/.mcp is the service user's: devbox-setup reads and writes it
// as mcp-ada, and mcp-sync runs as mcp-ada.
func TestMCPAsServiceUser(t *testing.T) {
	old := mcpAsService
	defer func() { mcpAsService = old }()
	mcpAsService = func(*Env) bool { return true }
	x := &fakeExec{}
	e := mcpEnv(t, x)
	e.Secrets.MCP["github GITHUB_PERSONAL_ACCESS_TOKEN"] = "ghp_1"
	apply(t, e, step(t, catalog.Step{Type: "go:mcp_servers"}))
	got := x.commands()
	if len(got) != 2 || !strings.HasPrefix(got[0], "(as mcp-ada) sh -c ") || !strings.HasSuffix(got[0], " _ /home/ada/.mcp/github/env") ||
		!strings.HasPrefix(got[1], "(as mcp-ada) sh -c 'umask 077") {
		t.Errorf("ran %q", got)
	}
	if in := string(x.ran[1].Stdin); !strings.Contains(in, "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_1\n") {
		t.Errorf("wrote %q", in)
	}
	x.ran = nil
	apply(t, e, step(t, catalog.Step{Type: "go:mcp_sync"}))
	if got := x.commands(); len(got) != 1 || !strings.HasPrefix(got[0], "(as mcp-ada) env MCP_DIR=/home/ada/.mcp ") {
		t.Errorf("sync ran %q", got)
	}
}

func TestMCPUserStep(t *testing.T) {
	exists := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		switch c.Name {
		case "id":
			if !exists {
				return nil, errors.New("no such user")
			}
			return []byte("mcp-ada docker\n"), nil
		case "stat":
			return []byte("mcp-ada 700\n"), nil
		case "getfacl":
			return []byte("user::rwx\nuser:mcp-ada:--x\ngroup::r-x\n"), nil
		}
		return nil, nil
	}}
	e := mcpEnv(t, x)
	st := step(t, catalog.Step{Type: "go:mcp_user"})
	if check(t, e, st) {
		t.Fatal("Check = true without the service user")
	}
	x.ran = nil
	apply(t, e, st)
	want := []string{
		"id -u mcp-ada",
		"useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin --comment 'MCPJungle gateway for ada' mcp-ada",
		"usermod -aG docker mcp-ada",
		"install -d -m 0700 -o mcp-ada -g mcp-ada /home/ada/.mcp",
		"chown -R mcp-ada:mcp-ada /home/ada/.mcp",
		"setfacl -m u:mcp-ada:x /home/ada",
	}
	if got := x.commands(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	exists = true
	if !check(t, e, st) {
		t.Error("Check = false with the user, its group, ~/.mcp and the ACL")
	}
}
