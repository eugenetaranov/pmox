package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() { RegisterGo("agentsafe", func(catalog.Step) Step { return agentSafeStep{} }) }

// agentSafeStep writes safety settings for unattended agents into each
// installed agent's own files, merged with what is there:
//   - Claude Code: permission rules denying force-pushes, sudo, and reads
//     of the MCP tokens, SSH keys and agent API keys;
//   - Codex: an approval policy and a workspace sandbox;
//   - both: a "Pushing" section in their notes (CLAUDE.md, AGENTS.md).
type agentSafeStep struct{}

// ClaudeDeny are the rules added to permissions.deny.
var ClaudeDeny = []string{
	"Bash(git push --force:*)",
	"Bash(git push -f:*)",
	"Bash(git push --force-with-lease:*)",
	"Bash(sudo:*)",
	"Read(~/.mcp/**)",
	"Read(~/.ssh/**)",
	"Read(~/.config/devbox/agent.env)",
}

// codexSafe are the top-level settings set in ~/.codex/config.toml.
var codexSafe = [][2]string{{"approval_policy", `"on-request"`}, {"sandbox_mode", `"workspace-write"`}}

const (
	pushBegin = "<!-- devbox-setup: agentsafe -->"
	pushEnd   = "<!-- /devbox-setup: agentsafe -->"
	pushing   = pushBegin + `
## Pushing

- Push only to a non-default branch, then open a pull request. Never push
  to main or master.
- Never force-push or rewrite history that was pushed.
` + pushEnd + "\n"
)

func (agentSafeStep) Plan(*Env) []Action {
	return []Action{
		{"file", "~/.claude/settings.json (permissions.deny: force-push, sudo, ~/.mcp, ~/.ssh, agent.env)"},
		{"file", "~/.codex/config.toml (approval_policy, sandbox_mode)"},
		{"file", "~/.claude/CLAUDE.md, ~/.codex/AGENTS.md (a Pushing section)"},
	}
}

// want returns each file to change, with its new content.
func (agentSafeStep) want(ctx context.Context, e *Env) (map[string][]byte, error) {
	out := map[string][]byte{}
	read := func(p string) ([]byte, error) {
		b, err := os.ReadFile(e.Path(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	if has(ctx, e, "claude") {
		cur, err := read("~/.claude/settings.json")
		if err != nil {
			return nil, err
		}
		if b, changed, err := claudeDeny(cur); err != nil {
			return nil, fmt.Errorf("~/.claude/settings.json: %w", err)
		} else if changed {
			out["~/.claude/settings.json"] = b
		}
		if err := withPushing(read, "~/.claude/CLAUDE.md", out); err != nil {
			return nil, err
		}
	}
	if has(ctx, e, "codex") {
		cur, err := read("~/.codex/config.toml")
		if err != nil {
			return nil, err
		}
		if b := codexSafeConfig(cur); !bytes.Equal(b, cur) {
			out["~/.codex/config.toml"] = b
		}
		if err := withPushing(read, "~/.codex/AGENTS.md", out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// claudeDeny merges ClaudeDeny into a settings.json's permissions.deny,
// keeping every other setting.
func claudeDeny(cur []byte) ([]byte, bool, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(cur)) > 0 {
		if err := json.Unmarshal(cur, &doc); err != nil {
			return nil, false, err
		}
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	deny, _ := perms["deny"].([]any)
	changed := false
	for _, r := range ClaudeDeny {
		if !slices.Contains(deny, any(r)) {
			deny, changed = append(deny, r), true
		}
	}
	if !changed {
		return cur, false, nil
	}
	perms["deny"] = deny
	doc["permissions"] = perms
	b, err := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n'), true, err
}

// codexSafeConfig sets codexSafe's keys at the top level of a Codex
// config: in place, or before the first table.
func codexSafeConfig(cur []byte) []byte {
	b := cur
	for _, kv := range codexSafe {
		line := kv[0] + " = " + kv[1]
		re := regexp.MustCompile(`(?m)^` + kv[0] + `\s*=.*$`)
		if re.Match(b) {
			b = re.ReplaceAll(b, []byte(line))
			continue
		}
		b = append([]byte(line+"\n"), b...)
	}
	return b
}

// withPushing adds (or refreshes) the Pushing section of an agent's notes.
func withPushing(read func(string) ([]byte, error), p string, out map[string][]byte) error {
	cur, err := read(p)
	if err != nil {
		return err
	}
	s := string(cur)
	if i, j := strings.Index(s, pushBegin), strings.Index(s, pushEnd); i >= 0 && j > i {
		s = s[:i] + pushing + strings.TrimPrefix(s[j+len(pushEnd):], "\n")
	} else {
		if s != "" && !strings.HasSuffix(s, "\n\n") {
			s = strings.TrimRight(s, "\n") + "\n\n"
		}
		s += pushing
	}
	if s != string(cur) {
		out[p] = []byte(s)
	}
	return nil
}

func (a agentSafeStep) Check(ctx context.Context, e *Env) (bool, error) {
	w, err := a.want(ctx, e)
	return err == nil && len(w) == 0, err
}

func (a agentSafeStep) Apply(ctx context.Context, e *Env) error {
	w, err := a.want(ctx, e)
	if err != nil {
		return err
	}
	for p, b := range w {
		for _, d := range []string{"~/.claude", "~/.codex"} {
			if strings.HasPrefix(p, d+"/") {
				if err := (mcpWireStep{}).userDir(e, d); err != nil {
					return err
				}
			}
		}
		mode := fs.FileMode(0o644)
		if fi, err := os.Stat(e.Path(p)); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := writeFile(e.Path(p), b, mode); err != nil {
			return err
		}
		if err := e.chown(e.Path(p), e.UID, e.GID); err != nil {
			return err
		}
	}
	return nil
}
