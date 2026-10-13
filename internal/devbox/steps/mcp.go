package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/mcp"
)

// MCPJungle: the gateway's port, and where its server catalog and
// mcp-sync go (the same places as tack-roles' ai-tools role).
const (
	MCPJunglePort = 8080
	mcpCatalog    = "/usr/local/share/mcp-catalog"
	mcpSync       = "/usr/local/bin/mcp-sync"
	mcpUnit       = "/etc/systemd/system/mcpjungle.service"
)

func init() {
	RegisterGo("mcp_files", func(catalog.Step) Step { return mcpFilesStep{} })
	RegisterGo("mcpjungle_unit", func(catalog.Step) Step { return mcpUnitStep{} })
	RegisterGo("mcp_wait", func(catalog.Step) Step { return mcpWaitStep{} })
	RegisterGo("mcp_servers", func(catalog.Step) Step { return mcpServersStep{} })
	RegisterGo("mcp_wire", func(catalog.Step) Step { return mcpWireStep{} })
	RegisterGo("mcp_sync", func(catalog.Step) Step { return mcpSyncStep{} })
}

func mcpURL() string { return fmt.Sprintf("http://127.0.0.1:%d", MCPJunglePort) }

// --- go:mcp_files: the server catalog and mcp-sync -------------------------

type mcpFilesStep struct{}

type wantFile struct {
	to   string
	b    []byte
	mode fs.FileMode
}

// files are the specs (pmox's own win over tack-roles' of the same name)
// and mcp-sync.
func (mcpFilesStep) files(e *Env) ([]wantFile, error) {
	specs := map[string][]byte{}
	for _, dir := range []string{filepath.Join(e.Share, "mcp-catalog"), filepath.Join(e.Share, "conf", "mcp-catalog")} {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.spec"))
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			specs[filepath.Base(p)] = b
		}
	}
	var out []wantFile
	for name, b := range specs {
		out = append(out, wantFile{filepath.Join(mcpCatalog, name), b, 0o644})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].to < out[j].to })
	b, err := os.ReadFile(filepath.Join(e.Share, "mcp-sync"))
	if err != nil {
		return nil, err
	}
	return append(out, wantFile{mcpSync, b, 0o755}), nil
}

func (mcpFilesStep) Plan(*Env) []Action {
	return []Action{{"file", mcpCatalog + "/*.spec"}, {"file", mcpSync}}
}

func same(p string, w wantFile) bool {
	cur, err := os.ReadFile(p)
	fi, serr := os.Stat(p)
	return err == nil && serr == nil && bytes.Equal(cur, w.b) && fi.Mode().Perm() == w.mode
}

func (m mcpFilesStep) Check(_ context.Context, e *Env) (bool, error) {
	files, err := m.files(e)
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if !same(e.Path(f.to), f) {
			return false, nil
		}
	}
	return true, nil
}

func (m mcpFilesStep) Apply(_ context.Context, e *Env) error {
	files, err := m.files(e)
	if err != nil {
		return err
	}
	for _, f := range files {
		if p := e.Path(f.to); !same(p, f) {
			if err := writeFile(p, f.b, f.mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- go:mcpjungle_unit -------------------------------------------------------

type mcpUnitStep struct{}

func (mcpUnitStep) Plan(*Env) []Action {
	return []Action{{"unit", "enable-now mcpjungle (" + mcpUnit + ")"}}
}

func listen(e *Env) string {
	if e.Answers != nil && e.Answers.MCPListen == "0.0.0.0" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// unit is the same unit as tack-roles' ai-tools role.
func (mcpUnitStep) unit(e *Env) []byte {
	dir := filepath.Join(e.Home, ".mcp")
	return []byte(fmt.Sprintf(`[Unit]
Description=MCPJungle MCP gateway
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=%s
SupplementaryGroups=docker
WorkingDirectory=%s
# Docker-based servers can take a while to start the first time.
Environment=MCP_SERVER_INIT_REQ_TIMEOUT_SEC=120
# --host is 127.0.0.1 (this machine) or 0.0.0.0 (all interfaces).
ExecStart=/usr/local/bin/mcpjungle start --host=%s --port %d --sqlite-db-path %s/mcpjungle.db
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, e.User, dir, listen(e), MCPJunglePort, dir))
}

func (u mcpUnitStep) Check(ctx context.Context, e *Env) (bool, error) {
	if !same(e.Path(mcpUnit), wantFile{b: u.unit(e), mode: 0o644}) {
		return false, nil
	}
	return systemctlStep{catalog.Step{Unit: "mcpjungle"}}.Check(ctx, e)
}

func (u mcpUnitStep) Apply(ctx context.Context, e *Env) error {
	w := wantFile{b: u.unit(e), mode: 0o644}
	if p := e.Path(mcpUnit); !same(p, w) {
		if err := writeFile(p, w.b, w.mode); err != nil {
			return err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", "mcpjungle"}, {"restart", "mcpjungle"}} {
			if _, err := e.run(ctx, Cmd{Name: "systemctl", Args: args}); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := e.run(ctx, Cmd{Name: "systemctl", Args: []string{"enable", "--now", "mcpjungle"}})
	return err
}

// --- go:mcp_wait: the gateway answers ----------------------------------------

type mcpWaitStep struct{}

// mcpUp reports whether the gateway answers (tests replace it).
var mcpUp = func(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mcpURL()+"/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// mcpWaitTries is how many seconds to wait for the gateway.
var mcpWaitTries = 60

func (mcpWaitStep) Plan(*Env) []Action { return []Action{{"wait", mcpURL()}} }

func (mcpWaitStep) Check(ctx context.Context, _ *Env) (bool, error) { return mcpUp(ctx), nil }

func (mcpWaitStep) Apply(ctx context.Context, e *Env) error {
	for i := 0; i < mcpWaitTries; i++ {
		if mcpUp(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	e.note(fmt.Sprintf("mcpjungle is not answering on port %d after %ds; see: journalctl -u mcpjungle", MCPJunglePort, mcpWaitTries))
	return nil
}

// --- go:mcp_servers: each picked server's env file ---------------------------

type mcpServersStep struct{}

func servers(e *Env) []string {
	if e.Answers == nil {
		return nil
	}
	return e.Answers.MCPServers
}

func (mcpServersStep) Plan(e *Env) []Action {
	var out []Action
	for _, n := range servers(e) {
		out = append(out, Action{"file", "~/.mcp/" + n + "/env (0600)"})
	}
	return out
}

// typed are the keys typed for server n this run.
func typed(e *Env, n string) map[string]string {
	out := map[string]string{}
	if e.Secrets == nil {
		return out
	}
	for k, v := range e.Secrets.MCP {
		if srv, key, ok := strings.Cut(k, " "); ok && srv == n {
			out[key] = v
		}
	}
	return out
}

func (mcpServersStep) Check(_ context.Context, e *Env) (bool, error) {
	for _, n := range servers(e) {
		p := e.Path("~/.mcp/" + n + "/env")
		b, err := os.ReadFile(p)
		if err != nil {
			return false, nil
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			return false, nil
		}
		for k, v := range typed(e, n) {
			if EnvGet(b, k) != v {
				return false, nil
			}
		}
	}
	return true, nil
}

func (mcpServersStep) Apply(ctx context.Context, e *Env) error {
	for _, n := range servers(e) {
		dir := e.Path("~/.mcp/" + n)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := errors.Join(os.Chmod(dir, 0o700), e.chown(dir, e.UID, e.GID)); err != nil {
			return err
		}
		p := filepath.Join(dir, "env")
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			spec, err := mcp.Load(ctx, mcp.Dirs(e.Share), n)
			if err != nil {
				return err
			}
			b = []byte(spec.Skeleton)
			if err := e.record("file", filepath.Join(e.Home, ".mcp", n, "env")); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		keys := typed(e, n)
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			b = EnvSet(b, k, keys[k])
		}
		if err := writeFile(p, b, 0o600); err != nil {
			return err
		}
		if err := e.chown(p, e.UID, e.GID); err != nil {
			return err
		}
	}
	return nil
}

// EnvGet returns the value of the last KEY= line in an env file.
func EnvGet(b []byte, key string) string {
	v := ""
	for _, l := range strings.Split(string(b), "\n") {
		if x, ok := strings.CutPrefix(l, key+"="); ok {
			v = x
		}
	}
	return v
}

// EnvSet sets KEY in an env file: in place of its first KEY= line (or of
// a commented "#KEY=" one), else appended. As the bash installer's envset.
func EnvSet(b []byte, key, value string) []byte {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	set := key + "=" + value
	done := false
	for i, l := range lines {
		commented := strings.HasPrefix(l, "#") && strings.HasPrefix(strings.TrimLeft(l[1:], " "), key+"=")
		if strings.HasPrefix(l, key+"=") || commented {
			lines[i], done = set, true
			break
		}
	}
	if !done {
		lines = append(lines, set)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// --- go:mcp_wire: the gateway in each installed agent ------------------------

type mcpWireStep struct{}

func wire(e *Env) bool {
	return e.Answers != nil && e.Answers.WireAgents != nil && *e.Answers.WireAgents
}

func agentURL() string { return fmt.Sprintf("http://localhost:%d/mcp", MCPJunglePort) }

func (mcpWireStep) Plan(e *Env) []Action {
	if !wire(e) {
		return nil
	}
	return []Action{{"run", "add the mcpjungle MCP server to Claude Code, Codex and Gemini CLI (those installed)"}}
}

// has reports whether the user can run tool (mise tools are only on
// their PATH).
func has(ctx context.Context, e *Env, tool string) bool {
	_, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "command -v " + tool}, AsUser: true})
	return err == nil
}

const codexSection = "[mcp_servers.mcpjungle]"

// todo lists the agents not wired yet.
func (mcpWireStep) todo(ctx context.Context, e *Env) []string {
	var out []string
	if has(ctx, e, "claude") {
		if _, err := e.Exec.Run(ctx, Cmd{Name: "claude", Args: []string{"mcp", "get", "mcpjungle"}, AsUser: true}); err != nil {
			out = append(out, "claude")
		}
	}
	if has(ctx, e, "codex") {
		b, _ := os.ReadFile(e.Path("~/.codex/config.toml"))
		if !hasLine(b, codexSection) {
			out = append(out, "codex")
		}
	}
	if has(ctx, e, "gemini") {
		if _, err := e.Exec.Run(ctx, Cmd{Name: "jq", Args: []string{"-e", ".mcpServers.mcpjungle", e.Path("~/.gemini/settings.json")}}); err != nil {
			out = append(out, "gemini")
		}
	}
	return out
}

func hasLine(b []byte, line string) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func (w mcpWireStep) Check(ctx context.Context, e *Env) (bool, error) {
	return !wire(e) || len(w.todo(ctx, e)) == 0, nil
}

func (w mcpWireStep) Apply(ctx context.Context, e *Env) error {
	if !wire(e) {
		return nil
	}
	for _, agent := range w.todo(ctx, e) {
		var err error
		switch agent {
		case "claude":
			_, err = e.run(ctx, Cmd{Name: "claude", Args: []string{"mcp", "add", "--transport", "http", "mcpjungle", agentURL(), "--scope", "user"}, AsUser: true})
		case "codex":
			err = w.codex(e)
		case "gemini":
			err = w.gemini(ctx, e)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", agent, err)
		}
	}
	return nil
}

func (mcpWireStep) userDir(e *Env, d string) error {
	p := e.Path(d)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

func (w mcpWireStep) codex(e *Env) error {
	if err := w.userDir(e, "~/.codex"); err != nil {
		return err
	}
	p := e.Path("~/.codex/config.toml")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	b = append(b, fmt.Sprintf("\n%s\nurl = %q\n", codexSection, agentURL())...)
	if err := writeFile(p, b, mode); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

func (w mcpWireStep) gemini(ctx context.Context, e *Env) error {
	if err := w.userDir(e, "~/.gemini"); err != nil {
		return err
	}
	p := e.Path("~/.gemini/settings.json")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	// jq keeps the user's key order and formatting choices.
	out, err := e.Exec.Run(ctx, Cmd{Name: "jq", Args: []string{"--arg", "u", agentURL(), ".mcpServers.mcpjungle = {httpUrl: $u}"}, Stdin: b})
	if err != nil {
		return fmt.Errorf("jq: %w: %s", err, out)
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := writeFile(p, out, mode); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

// --- go:mcp_sync: register the picked servers with the gateway ---------------

// mcpSyncStep runs mcp-sync, which registers servers whose keys are filled
// in and re-registers those whose config changed: on every run.
type mcpSyncStep struct{}

func (mcpSyncStep) Always() bool { return true }

func (mcpSyncStep) Plan(e *Env) []Action {
	return []Action{{"run", strings.TrimSpace("mcp-sync " + strings.Join(servers(e), " "))}}
}

func (mcpSyncStep) Check(context.Context, *Env) (bool, error) { return true, nil }

func (mcpSyncStep) Apply(ctx context.Context, e *Env) error {
	args := append([]string{"MCP_CATALOG=" + mcpCatalog, "MCPJUNGLE_REGISTRY=" + mcpURL(), mcpSync}, servers(e)...)
	if _, err := e.run(ctx, Cmd{Name: "env", Args: args, AsUser: true}); err != nil {
		return fmt.Errorf("some MCP servers did not register (see the log); fill in ~/.mcp/<name>/env, then run mcp-sync: %w", err)
	}
	return nil
}
