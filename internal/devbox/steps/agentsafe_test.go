package steps

import (
	"errors"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func TestAgentSafe(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "sh" && (strings.HasSuffix(c.Args[1], " claude") || strings.HasSuffix(c.Args[1], " codex")) {
			return nil, nil
		}
		return nil, errors.New("no")
	}}
	e := testEnv(t, x)
	put(t, e.Path("~/.claude/settings.json"), `{"env": {"FOO": "1"}, "permissions": {"deny": ["Bash(rm -rf /:*)"]}}`)
	put(t, e.Path("~/.codex/config.toml"), "model = \"o4\"\napproval_policy = \"never\"\n\n[mcp_servers.mcpjungle]\nurl = \"x\"\n")
	put(t, e.Path("~/.claude/CLAUDE.md"), "# This machine\n")
	st := step(t, catalog.Step{Type: "go:agentsafe"})
	if check(t, e, st) {
		t.Fatal("Check = true before Apply")
	}
	apply(t, e, st)
	cs := read(t, e.Path("~/.claude/settings.json"))
	for _, want := range []string{`"FOO": "1"`, `"Bash(rm -rf /:*)"`, `"Bash(git push --force:*)"`, `"Read(~/.mcp/**)"`, `"Bash(sudo:*)"`} {
		if !strings.Contains(cs, want) {
			t.Errorf("settings.json lacks %s:\n%s", want, cs)
		}
	}
	if got := read(t, e.Path("~/.codex/config.toml")); got != "sandbox_mode = \"workspace-write\"\nmodel = \"o4\"\napproval_policy = \"on-request\"\n\n[mcp_servers.mcpjungle]\nurl = \"x\"\n" {
		t.Errorf("config.toml = %q", got)
	}
	md := read(t, e.Path("~/.claude/CLAUDE.md"))
	if !strings.HasPrefix(md, "# This machine\n\n"+pushBegin) || !strings.HasSuffix(md, pushEnd+"\n") {
		t.Errorf("CLAUDE.md = %q", md)
	}
	if got := read(t, e.Path("~/.codex/AGENTS.md")); got != pushing {
		t.Errorf("AGENTS.md = %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check = false after Apply")
	}
	// The section is refreshed in place, not added twice.
	put(t, e.Path("~/.claude/CLAUDE.md"), "# This machine\n\n"+pushBegin+"\nold\n"+pushEnd+"\nmine\n")
	apply(t, e, st)
	if got := read(t, e.Path("~/.claude/CLAUDE.md")); got != "# This machine\n\n"+pushing+"mine\n" {
		t.Errorf("refreshed CLAUDE.md = %q", got)
	}
}
