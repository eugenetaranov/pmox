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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/mcp"
)

// MCPJungle runs per user, as mcpjungle@<user> on the user's port (from
// system.json). Its server catalog and mcp-sync are shared (the same
// places as tack-roles' ai-tools role).
const (
	mcpCatalog = "/usr/local/share/mcp-catalog"
	mcpSync    = "/usr/local/bin/mcp-sync"
	mcpUnit    = "/etc/systemd/system/mcpjungle@.service"
	mcpEnvDir  = "/etc/mcpjungle"
	// oldMCPUnit is Phase 1's single gateway, replaced by its user's instance.
	oldMCPUnit = "/etc/systemd/system/mcpjungle.service"
)

func init() {
	RegisterGo("mcp_files", func(catalog.Step) Step { return mcpFilesStep{} })
	RegisterGo("mcpjungle_unit", func(catalog.Step) Step { return mcpUnitStep{} })
	RegisterGo("mcp_wait", func(catalog.Step) Step { return mcpWaitStep{} })
	RegisterGo("mcp_servers", func(catalog.Step) Step { return mcpServersStep{} })
	RegisterGo("mcp_wire", func(catalog.Step) Step { return mcpWireStep{} })
	RegisterGo("mcp_sync", func(catalog.Step) Step { return mcpSyncStep{} })
	RegisterGo("mcp_user", func(catalog.Step) Step { return mcpUserStep{} })
}

// MCPUser is the system user the user's gateway runs as. It owns
// ~/.mcp, so the login user (and agents running as them) can't read the
// MCP tokens without sudo.
func MCPUser(e *Env) string { return "mcp-" + e.User }

// mcpAsService says whether ~/.mcp is reached as the service user (as
// root, on a VM) or directly (tests, run as a plain user).
var mcpAsService = func(e *Env) bool { return e.Chown }

// mcpRead reads ~/.mcp/rel as the service user; nil and no error when it
// doesn't exist. As that user, a symlink planted by the login user leads
// nowhere it couldn't already read.
func mcpRead(ctx context.Context, e *Env, rel string) ([]byte, error) {
	p := filepath.Join(e.Home, ".mcp", rel)
	if !mcpAsService(e) {
		b, err := os.ReadFile(e.Path(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	// "x" marks a file that is there (an empty one included).
	out, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", `[ -e "$1" ] || exit 0; printf x; cat -- "$1"`, "_", p}, As: MCPUser(e)})
	if err != nil {
		return nil, fmt.Errorf("read %s as %s: %w", p, MCPUser(e), err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[1:], nil
}

// mcpWrite writes ~/.mcp/rel (0600, its directory 0700) as the service user.
func mcpWrite(ctx context.Context, e *Env, rel string, b []byte) error {
	p := filepath.Join(e.Home, ".mcp", rel)
	if !mcpAsService(e) {
		dir := filepath.Dir(e.Path(p))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := errors.Join(os.Chmod(dir, 0o700), e.chown(dir, e.UID, e.GID)); err != nil {
			return err
		}
		if err := writeFile(e.Path(p), b, 0o600); err != nil {
			return err
		}
		return e.chown(e.Path(p), e.UID, e.GID)
	}
	_, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c",
		`umask 077 && mkdir -p -- "$(dirname -- "$1")" && cat > "$1.tmp" && chmod 600 "$1.tmp" && mv -f -- "$1.tmp" "$1"`, "_", p},
		Stdin: b, As: MCPUser(e)})
	return err
}

// --- go:mcp_user: the gateway's service user, owning ~/.mcp -----------------

type mcpUserStep struct{}

func (mcpUserStep) Plan(e *Env) []Action {
	return []Action{{"run", fmt.Sprintf("system user %s (docker group) owns ~/.mcp; it may pass through %s (ACL)", MCPUser(e), e.Home)}}
}

func (mcpUserStep) Check(ctx context.Context, e *Env) (bool, error) {
	svc := MCPUser(e)
	groups, err := e.Exec.Run(ctx, Cmd{Name: "id", Args: []string{"-nG", svc}})
	if err != nil || !slices.Contains(strings.Fields(string(groups)), "docker") {
		return false, nil
	}
	owner, err := e.Exec.Run(ctx, Cmd{Name: "stat", Args: []string{"-c", "%U %a", filepath.Join(e.Home, ".mcp")}})
	if err != nil || strings.TrimSpace(string(owner)) != svc+" 700" {
		return false, nil
	}
	acl, err := e.Exec.Run(ctx, Cmd{Name: "getfacl", Args: []string{"-cp", e.Home}})
	return err == nil && hasLine(acl, "user:"+svc+":--x"), nil
}

func (mcpUserStep) Apply(ctx context.Context, e *Env) error {
	svc := MCPUser(e)
	if len(svc) > 32 {
		return fmt.Errorf("%s: user names are at most 32 characters; the gateway needs a shorter login name", svc)
	}
	dir := filepath.Join(e.Home, ".mcp")
	if _, err := e.Exec.Run(ctx, Cmd{Name: "id", Args: []string{"-u", svc}}); err != nil {
		if _, err := e.run(ctx, Cmd{Name: "useradd", Args: []string{"--system", "--user-group", "--no-create-home",
			"--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "--comment", "MCPJungle gateway for " + e.User, svc}}); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{"usermod", "-aG", "docker", svc},
		// The login user's own ~/.mcp from before: the service user's now
		// (chown -R doesn't follow symlinks).
		{"install", "-d", "-m", "0700", "-o", svc, "-g", svc, dir},
		{"chown", "-R", svc + ":" + svc, dir},
		{"setfacl", "-m", "u:" + svc + ":x", e.Home},
	} {
		if _, err := e.run(ctx, Cmd{Name: args[0], Args: args[1:]}); err != nil {
			return err
		}
	}
	return nil
}

// MCPPort is the user's gateway port.
func MCPPort(e *Env) int {
	if e.Answers != nil && e.Answers.MCPPort > 0 {
		return e.Answers.MCPPort
	}
	return answers.FirstMCPPort
}

func mcpURL(e *Env) string { return fmt.Sprintf("http://127.0.0.1:%d", MCPPort(e)) }

// mcpInstance is the user's gateway unit.
func mcpInstance(e *Env) string { return "mcpjungle@" + e.User }

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

func (mcpUnitStep) Plan(e *Env) []Action {
	return []Action{{"unit", fmt.Sprintf("enable-now %s on port %d (%s)", mcpInstance(e), MCPPort(e), mcpUnit)}}
}

func listen(e *Env) string {
	if e.Answers != nil && e.Answers.MCPListen == "0.0.0.0" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// mcpTemplate is the gateway's template unit: one instance per user, each
// with its host, port and data directory in /etc/mcpjungle/<user>.env.
const mcpTemplate = `[Unit]
Description=MCPJungle MCP gateway for %i
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
# The service user owns ~<user>/.mcp: the login user can't read the tokens.
User=mcp-%i
SupplementaryGroups=docker
EnvironmentFile=/etc/mcpjungle/%i.env
# Docker-based servers can take a while to start the first time.
Environment=MCP_SERVER_INIT_REQ_TIMEOUT_SEC=120
# MCP_HOST is 127.0.0.1 (this machine) or 0.0.0.0 (all interfaces).
ExecStart=/usr/local/bin/mcpjungle start --host=${MCP_HOST} --port ${MCP_PORT} --sqlite-db-path ${MCP_DIR}/mcpjungle.db
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

func (mcpUnitStep) envFile(e *Env) wantFile {
	return wantFile{filepath.Join(mcpEnvDir, e.User+".env"),
		[]byte(fmt.Sprintf("MCP_HOST=%s\nMCP_PORT=%d\nMCP_DIR=%s\n", listen(e), MCPPort(e), filepath.Join(e.Home, ".mcp"))), 0o644}
}

// oldUnit reports whether Phase 1's single gateway runs as this user.
func oldUnit(e *Env) bool {
	b, err := os.ReadFile(e.Path(oldMCPUnit))
	return err == nil && hasLine(b, "User="+e.User)
}

func (u mcpUnitStep) Check(ctx context.Context, e *Env) (bool, error) {
	env := u.envFile(e)
	if !same(e.Path(mcpUnit), wantFile{b: []byte(mcpTemplate), mode: 0o644}) || !same(e.Path(env.to), env) || oldUnit(e) {
		return false, nil
	}
	return systemctlStep{catalog.Step{Unit: mcpInstance(e)}}.Check(ctx, e)
}

func (u mcpUnitStep) Apply(ctx context.Context, e *Env) error {
	var cmds [][]string
	if oldUnit(e) {
		// Phase 1's gateway: same user, same database; the instance replaces it.
		if _, err := e.run(ctx, Cmd{Name: "systemctl", Args: []string{"disable", "--now", "mcpjungle"}}); err != nil {
			return err
		}
		if err := os.Remove(e.Path(oldMCPUnit)); err != nil {
			return err
		}
		cmds = append(cmds, []string{"daemon-reload"})
	}
	changed := false
	for _, w := range []wantFile{{mcpUnit, []byte(mcpTemplate), 0o644}, u.envFile(e)} {
		if p := e.Path(w.to); !same(p, w) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := writeFile(p, w.b, w.mode); err != nil {
				return err
			}
			changed = true
		}
	}
	inst := mcpInstance(e)
	if changed {
		if len(cmds) == 0 {
			cmds = append(cmds, []string{"daemon-reload"})
		}
		cmds = append(cmds, []string{"enable", inst}, []string{"restart", inst})
	} else {
		cmds = append(cmds, []string{"enable", "--now", inst})
	}
	for _, args := range cmds {
		if _, err := e.run(ctx, Cmd{Name: "systemctl", Args: args}); err != nil {
			return err
		}
	}
	return nil
}

// --- go:mcp_wait: the gateway answers ----------------------------------------

type mcpWaitStep struct{}

// mcpUp reports whether the gateway at url answers (tests replace it).
var mcpUp = func(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/", nil)
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

func (mcpWaitStep) Plan(e *Env) []Action { return []Action{{"wait", mcpURL(e)}} }

func (mcpWaitStep) Check(ctx context.Context, e *Env) (bool, error) {
	return mcpUp(ctx, mcpURL(e)), nil
}

func (mcpWaitStep) Apply(ctx context.Context, e *Env) error {
	for i := 0; i < mcpWaitTries; i++ {
		if mcpUp(ctx, mcpURL(e)) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	e.note(fmt.Sprintf("mcpjungle is not answering on port %d after %ds; see: journalctl -u %s", MCPPort(e), mcpWaitTries, mcpInstance(e)))
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

func (mcpServersStep) Check(ctx context.Context, e *Env) (bool, error) {
	for _, n := range servers(e) {
		b, err := mcpRead(ctx, e, n+"/env")
		if err != nil || b == nil {
			return false, nil
		}
		if !mcpAsService(e) {
			if fi, _ := os.Stat(e.Path(filepath.Join(e.Home, ".mcp", n, "env"))); fi.Mode().Perm() != 0o600 {
				return false, nil
			}
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
		b, err := mcpRead(ctx, e, n+"/env")
		if err != nil {
			return err
		}
		if b == nil {
			spec, err := mcp.Load(ctx, mcp.Dirs(e.Share), n)
			if err != nil {
				return err
			}
			b = []byte(spec.Skeleton)
			if err := e.record("file", filepath.Join(e.Home, ".mcp", n, "env")); err != nil {
				return err
			}
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
		if err := mcpWrite(ctx, e, n+"/env", b); err != nil {
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

func agentURL(e *Env) string { return fmt.Sprintf("http://localhost:%d/mcp", MCPPort(e)) }

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

// todo lists the agents not wired to the user's gateway yet: not at all
// ("claude"), or to another port ("claude!", Claude Code only, which
// needs a remove first; Codex and Gemini are rewritten in place).
func (mcpWireStep) todo(ctx context.Context, e *Env) []string {
	var out []string
	url := agentURL(e)
	if has(ctx, e, "claude") {
		got, err := e.Exec.Run(ctx, Cmd{Name: "claude", Args: []string{"mcp", "get", "mcpjungle"}, AsUser: true})
		switch {
		case err != nil:
			out = append(out, "claude")
		case !strings.Contains(string(got), url):
			out = append(out, "claude!")
		}
	}
	if has(ctx, e, "codex") {
		b, _ := os.ReadFile(e.Path("~/.codex/config.toml"))
		if !bytes.Equal(codexWired(b, url), b) {
			out = append(out, "codex")
		}
	}
	if has(ctx, e, "gemini") {
		if _, err := e.Exec.Run(ctx, Cmd{Name: "jq", Args: []string{"-e", "--arg", "u", url, ".mcpServers.mcpjungle.httpUrl == $u", e.Path("~/.gemini/settings.json")}}); err != nil {
			out = append(out, "gemini")
		}
	}
	return out
}

// codexWired returns Codex's config with the mcpjungle section pointing
// at url: its url line replaced, or the section appended.
func codexWired(b []byte, url string) []byte {
	want := fmt.Sprintf("url = %q", url)
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if l != codexSection {
			continue
		}
		for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "["); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "url") {
				lines[j] = want
				return []byte(strings.Join(lines, "\n"))
			}
		}
		lines = slices.Insert(lines, i+1, want)
		return []byte(strings.Join(lines, "\n"))
	}
	return append(append([]byte{}, b...), fmt.Sprintf("\n%s\n%s\n", codexSection, want)...)
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
		case "claude!":
			if _, err = e.run(ctx, Cmd{Name: "claude", Args: []string{"mcp", "remove", "mcpjungle", "--scope", "user"}, AsUser: true}); err != nil {
				break
			}
			fallthrough
		case "claude":
			_, err = e.run(ctx, Cmd{Name: "claude", Args: []string{"mcp", "add", "--transport", "http", "mcpjungle", agentURL(e), "--scope", "user"}, AsUser: true})
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
	if err := writeFile(p, codexWired(b, agentURL(e)), mode); err != nil {
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
	out, err := e.Exec.Run(ctx, Cmd{Name: "jq", Args: []string{"--arg", "u", agentURL(e), ".mcpServers.mcpjungle = {httpUrl: $u}"}, Stdin: b})
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
	args := append([]string{"MCP_DIR=" + filepath.Join(e.Home, ".mcp"), "MCP_CATALOG=" + mcpCatalog, "MCPJUNGLE_REGISTRY=" + mcpURL(e), mcpSync}, servers(e)...)
	c := Cmd{Name: "env", Args: args, As: MCPUser(e)}
	if !mcpAsService(e) {
		c = Cmd{Name: "env", Args: args, AsUser: true}
	}
	if _, err := e.run(ctx, c); err != nil {
		return fmt.Errorf("some MCP servers did not register (see the log); fill in the keys with 'mcp-sync edit <server>', then run mcp-sync: %w", err)
	}
	return nil
}
