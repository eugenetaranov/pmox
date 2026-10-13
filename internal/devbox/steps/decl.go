package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// --- apt ---------------------------------------------------------------------

type aptStep struct{ s catalog.Step }

func (a aptStep) Plan(e *Env) []Action {
	pkgs := a.Packages(e)
	for i, p := range pkgs {
		if v := e.lockedApt(p); v != "" {
			pkgs[i] = p + "=" + v
		}
	}
	return []Action{{"apt", strings.Join(pkgs, " ")}}
}

func (a aptStep) Check(ctx context.Context, e *Env) (bool, error) {
	missing, err := MissingPackages(ctx, e, a.Packages(e))
	return len(missing) == 0, err
}

func (a aptStep) Apply(ctx context.Context, e *Env) error { return AptInstall(ctx, e, a.Packages(e)) }

// Packages returns the apt step's packages, with {{kernel}} filled in
// (the runner batches them).
func (a aptStep) Packages(e *Env) []string {
	out := make([]string, len(a.s.Packages))
	for i, p := range a.s.Packages {
		out[i] = strings.ReplaceAll(p, "{{kernel}}", e.Kernel)
	}
	return out
}

// MissingPackages returns the packages that aren't installed.
func MissingPackages(ctx context.Context, e *Env, pkgs []string) ([]string, error) {
	var missing []string
	for _, p := range pkgs {
		out, err := e.Exec.Run(ctx, Cmd{Name: "dpkg-query", Args: []string{"-W", "-f=${Status}", p}})
		if err != nil || !strings.Contains(string(out), "install ok installed") {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

// AptOpts are apt-get's options for unattended installs: wait for another
// apt's lock, and keep existing config files instead of asking.
var AptOpts = []string{"-q", "-y", "-o", "DPkg::Lock::Timeout=900",
	"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

// AptEnv keeps apt quiet: no questions, and needrestart lists instead of
// restarting (a docker restart mid-run would kill MCP containers).
var AptEnv = []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"}

// AptInstall installs pkgs, at their locked versions while the archive
// still has them (otherwise the current one, with a note).
func AptInstall(ctx context.Context, e *Env, pkgs []string) error {
	if len(pkgs) == 0 {
		return nil
	}
	args := append(append([]string{}, AptOpts...), "install")
	for _, p := range pkgs {
		v := e.lockedApt(p)
		switch {
		case v == "":
			args = append(args, p)
		case aptHasVersion(ctx, e, p, v):
			args = append(args, p+"="+v)
		default:
			e.note(fmt.Sprintf("apt: %s %s (locked) is no longer in the archive; installed the current version", p, v))
			args = append(args, p)
		}
	}
	_, err := e.run(ctx, Cmd{Name: "apt-get", Args: args, Env: AptEnv})
	return err
}

// aptHasVersion reports whether the package lists have pkg at version v.
func aptHasVersion(ctx context.Context, e *Env, pkg, v string) bool {
	out, err := e.Exec.Run(ctx, Cmd{Name: "apt-cache", Args: []string{"show", "--no-all-versions", pkg + "=" + v}})
	return err == nil && strings.Contains(string(out), "Version: "+v)
}

func (e *Env) lockedApt(pkg string) string {
	if e.Lock == nil {
		return ""
	}
	return e.Lock.Apt[pkg]
}

// AptUpdate refreshes the package lists. DPkg::Lock::Timeout covers
// installs, not update's lists lock, which cloud-init or apt-daily can
// hold right after boot: it retries while another apt holds it, for up
// to 15 minutes.
func AptUpdate(ctx context.Context, e *Env) error {
	for tries := 0; ; tries++ {
		out, err := e.run(ctx, Cmd{Name: "apt-get", Args: append(append([]string{}, AptOpts...), "update"), Env: AptEnv})
		if err == nil || !strings.Contains(string(out), "Could not get lock") || tries >= 180 {
			return err
		}
		if e.Log != nil && tries == 0 {
			fmt.Fprintln(e.Log, "Waiting for another apt process to finish…")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(AptRetryDelay):
		}
	}
}

// AptRetryDelay is the wait between apt-get update tries (tests shorten it).
var AptRetryDelay = 5 * time.Second

// --- mise --------------------------------------------------------------------

type miseStep struct{ s catalog.Step }

func (m miseStep) Plan(e *Env) []Action {
	return []Action{{"mise", strings.Join(LockedTools(e, m.s.Tools), " ")}}
}

// LockedTools returns tools with each locked one's version replaced by
// the lock's.
func LockedTools(e *Env, tools []string) []string {
	if e.Lock == nil || len(e.Lock.Mise) == 0 {
		return tools
	}
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t
		if v := e.Lock.Mise[MiseName(t)]; v != "" {
			if j := strings.LastIndexByte(t, '@'); j > 0 && j > strings.LastIndexByte(t, ']') {
				t = t[:j]
			}
			out[i] = t + "@" + v
		}
	}
	return out
}

// MiseName is the name `mise ls` reports for a tool spec: no version, no
// [options].
func MiseName(spec string) string {
	if i := strings.IndexByte(spec, '['); i >= 0 {
		spec = spec[:i]
	}
	if i := strings.LastIndexByte(spec, '@'); i > 0 {
		spec = spec[:i]
	}
	return spec
}

func (m miseStep) Check(ctx context.Context, e *Env) (bool, error) { return MiseHas(ctx, e, m.s.Tools) }

// MiseHas reports whether every tool is in the user's global mise config,
// at its locked version when there is one.
func MiseHas(ctx context.Context, e *Env, tools []string) (bool, error) {
	have, ok := miseList(ctx, e)
	if !ok {
		return false, nil
	}
	for _, t := range tools {
		name := MiseName(t)
		vs, ok := have[name]
		if !ok {
			return false, nil
		}
		if e.Lock != nil && e.Lock.Mise[name] != "" && !slices.ContainsFunc(vs, func(v miseVersion) bool { return v.Version == e.Lock.Mise[name] }) {
			return false, nil
		}
	}
	return true, nil
}

type miseVersion struct {
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
}

// miseList is `mise ls --global --json`: tool → its versions.
func miseList(ctx context.Context, e *Env) (map[string][]miseVersion, bool) {
	out, err := e.Exec.Run(ctx, Cmd{Name: "mise", Args: []string{"ls", "--global", "--json"}, AsUser: true})
	if err != nil {
		return nil, false
	}
	var have map[string][]miseVersion
	if json.Unmarshal(out, &have) != nil {
		return nil, false
	}
	return have, true
}

// Versions reads the installed versions of mise tools (the active
// one) and apt packages, for `export --lock`. missing lists what isn't
// installed (so isn't locked).
func Versions(ctx context.Context, e *Env, tools, pkgs []string) (l *answers.Lock, missing []string) {
	l = &answers.Lock{Mise: map[string]string{}, Apt: map[string]string{}}
	have, _ := miseList(ctx, e)
	for _, t := range tools {
		name := MiseName(t)
		v := ""
		for _, x := range have[name] {
			if x.Installed && (v == "" || x.Active) {
				v = x.Version
			}
		}
		if v == "" {
			missing = append(missing, name)
			continue
		}
		l.Mise[name] = v
	}
	for _, p := range pkgs {
		out, err := e.Exec.Run(ctx, Cmd{Name: "dpkg-query", Args: []string{"-W", "-f=${Status}\t${Version}", p}})
		st, v, _ := strings.Cut(string(out), "\t")
		if err != nil || !strings.Contains(st, "install ok installed") || v == "" {
			missing = append(missing, p)
			continue
		}
		l.Apt[p] = v
	}
	return l, missing
}

func (m miseStep) Apply(ctx context.Context, e *Env) error { return MiseUse(ctx, e, m.s.Tools) }

// MiseUse installs tools globally for the user, at their locked versions.
func MiseUse(ctx context.Context, e *Env, tools []string) error {
	if _, err := e.run(ctx, Cmd{Name: "mise", Args: append([]string{"use", "-g", "--yes"}, LockedTools(e, tools)...), AsUser: true}); err != nil {
		return err
	}
	_, err := e.run(ctx, Cmd{Name: "mise", Args: []string{"reshim"}, AsUser: true})
	return err
}

// --- apt_repo ----------------------------------------------------------------

type aptRepoStep struct{ s catalog.Step }

const keyrings = "/etc/apt/keyrings"

// Repo marks the step for the runner's apt batch.
func (aptRepoStep) Repo() bool { return true }

func (r aptRepoStep) keyPath() string { return filepath.Join(keyrings, r.s.Name+".asc") }
func (r aptRepoStep) listPath() string {
	return filepath.Join("/etc/apt/sources.list.d", r.s.Name+".sources")
}

func (r aptRepoStep) source(e *Env) string {
	s := strings.NewReplacer("{{arch}}", e.Arch, "{{codename}}", e.Codename, "{{keyring}}", r.keyPath()).Replace(r.s.Source)
	return strings.TrimSpace(s) + "\n"
}

func (r aptRepoStep) Plan(*Env) []Action {
	return []Action{{"repo", r.listPath() + " (key " + r.s.KeyURL + ")"}}
}

func (r aptRepoStep) Check(_ context.Context, e *Env) (bool, error) {
	if _, err := os.Stat(e.Path(r.keyPath())); err != nil {
		return false, nil
	}
	b, err := os.ReadFile(e.Path(r.listPath()))
	return err == nil && string(b) == r.source(e), nil
}

func (r aptRepoStep) Apply(ctx context.Context, e *Env) error {
	kp := e.Path(r.keyPath())
	if _, err := os.Stat(kp); err != nil {
		if e.Download == nil {
			return errors.New("no downloader")
		}
		key, err := e.Download(ctx, r.s.KeyURL)
		if err != nil {
			return fmt.Errorf("download the signing key %s: %w", r.s.KeyURL, err)
		}
		if len(key) == 0 {
			return fmt.Errorf("download the signing key %s: empty", r.s.KeyURL)
		}
		if r.s.KeyFingerprint != "" {
			if err := checkFingerprint(ctx, e, key, r.s.KeyFingerprint); err != nil {
				return fmt.Errorf("%s: %w", r.s.KeyURL, err)
			}
		}
		if err := writeFile(kp, key, 0o644); err != nil {
			return err
		}
	}
	return writeFile(e.Path(r.listPath()), []byte(r.source(e)), 0o644)
}

// checkFingerprint checks that key (armored or binary) is the key with fpr.
func checkFingerprint(ctx context.Context, e *Env, key []byte, fpr string) error {
	out, err := e.Exec.Run(ctx, Cmd{Name: "gpg", Args: []string{"--show-keys", "--with-colons"}, Stdin: key})
	if err != nil {
		return fmt.Errorf("gpg --show-keys: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ":"); len(f) > 9 && f[0] == "fpr" && f[9] == fpr {
			return nil
		}
	}
	return fmt.Errorf("signing key fingerprint is not %s", fpr)
}

// --- file / template ---------------------------------------------------------

type fileStep struct {
	s        catalog.Step
	template bool
}

func (f fileStep) Plan(*Env) []Action { return []Action{{"file", f.s.To}} }

func (f fileStep) mode() fs.FileMode {
	if f.s.Mode == "" {
		return 0o644
	}
	m, _ := strconv.ParseUint(f.s.Mode, 8, 32)
	return fs.FileMode(m)
}

func (f fileStep) content(e *Env) ([]byte, error) {
	b, err := fs.ReadFile(e.Files, f.s.Src)
	if err != nil || !f.template {
		return b, err
	}
	t, err := template.New(f.s.Src).Option("missingkey=error").Parse(string(b))
	if err != nil {
		return nil, err
	}
	vars := map[string]string{"user": e.User, "home": e.Home}
	for k, v := range f.s.Vars {
		vars[k] = v
	}
	var out bytes.Buffer
	if err := t.Execute(&out, vars); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (f fileStep) Check(_ context.Context, e *Env) (bool, error) {
	p := e.Path(f.s.To)
	cur, err := os.ReadFile(p)
	if err != nil {
		return false, nil
	}
	if f.s.IfAbsent {
		return true, nil
	}
	want, err := f.content(e)
	if err != nil {
		return false, err
	}
	fi, _ := os.Stat(p)
	return bytes.Equal(cur, want) && fi.Mode().Perm() == f.mode(), nil
}

func (f fileStep) Apply(_ context.Context, e *Env) error {
	p := e.Path(f.s.To)
	want, err := f.content(e)
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(p)
	if err == nil && f.s.IfAbsent {
		return nil
	}
	if err == nil && f.s.KeepOrig && !bytes.Equal(cur, want) {
		if _, err := os.Stat(p + ".orig"); errors.Is(err, fs.ErrNotExist) {
			fi, _ := os.Stat(p)
			if err := writeFile(p+".orig", cur, fi.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	if err := mkdirFor(e, p, f.s); err != nil {
		return err
	}
	if err := writeFile(p, want, f.mode()); err != nil {
		return err
	}
	uid, gid, _ := e.owner(f.s)
	return e.chown(p, uid, gid)
}

// mkdirFor creates p's directory, owned like the file under the home.
func mkdirFor(e *Env, p string, s catalog.Step) error {
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	uid, gid, user := e.owner(s)
	home := e.Path("~")
	if !user {
		return nil
	}
	for d := dir; strings.HasPrefix(d, home+"/"); d = filepath.Dir(d) {
		if err := e.chown(d, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes b to p atomically with mode.
func writeFile(p string, b []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), mode)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// --- line --------------------------------------------------------------------

type lineStep struct{ s catalog.Step }

func (l lineStep) Plan(*Env) []Action { return []Action{{"line", l.s.To + ": " + l.s.Line}} }

// want is the file with the line in it, or nil when the file is missing
// and IfExists says to leave it.
func (l lineStep) want(cur []byte, missing bool) []byte {
	if missing && l.s.IfExists {
		return nil
	}
	if l.s.Top {
		return withLineFirst(cur, l.s.Line)
	}
	return WithLine(cur, l.s.Line, l.s.Match)
}

func (l lineStep) Check(_ context.Context, e *Env) (bool, error) {
	cur, err := os.ReadFile(e.Path(l.s.To))
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return false, err
	}
	want := l.want(cur, missing)
	return want == nil || (!missing && bytes.Equal(cur, want)), nil
}

func (l lineStep) Apply(_ context.Context, e *Env) error {
	p := e.Path(l.s.To)
	cur, err := os.ReadFile(p)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	want := l.want(cur, missing)
	if want == nil {
		return nil
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	} else if err := mkdirFor(e, p, l.s); err != nil {
		return err
	}
	if err := writeFile(p, want, mode); err != nil {
		return err
	}
	uid, gid, _ := e.owner(l.s)
	return e.chown(p, uid, gid)
}

// LastMark starts the block that sources a user's local override file;
// it stays at the end of the file (WithLine adds lines before it).
const LastMark = "# devbox: local overrides (keep last)"

// WithLine returns b with line in it: in place of every line matching the
// regexp match (when there is one and it matches), else appended after a
// blank line (none in an empty file), unless b already has it. An
// appended line goes before the LastMark block when there is one.
func WithLine(b []byte, line, match string) []byte {
	lines := strings.Split(string(b), "\n")
	if match != "" {
		re := regexp.MustCompile(match)
		hit := false
		for i, x := range lines {
			if re.MatchString(x) {
				lines[i], hit = line, true
			}
		}
		if hit {
			return []byte(strings.Join(lines, "\n"))
		}
	}
	for _, x := range lines {
		if x == line {
			return b
		}
	}
	if len(b) == 0 {
		return []byte(line + "\n")
	}
	if i := slices.Index(lines, LastMark); i >= 0 {
		return []byte(strings.Join(slices.Insert(lines, i, line, ""), "\n"))
	}
	return append(append([]byte{}, b...), "\n"+line+"\n"...)
}

// withLineFirst returns b with line as its first line, unless b already
// has it.
func withLineFirst(b []byte, line string) []byte {
	for _, x := range strings.Split(string(b), "\n") {
		if x == line {
			return b
		}
	}
	return append([]byte(line+"\n"), b...)
}

// --- link --------------------------------------------------------------------

type linkStep struct{ s catalog.Step }

func (l linkStep) Plan(*Env) []Action { return []Action{{"link", l.s.To + " → " + l.s.Src}} }

func (l linkStep) Check(_ context.Context, e *Env) (bool, error) {
	got, err := os.Readlink(e.Path(l.s.To))
	return err == nil && got == l.s.Src, nil
}

func (l linkStep) Apply(_ context.Context, e *Env) error {
	p := e.Path(l.s.To)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	_ = os.Remove(p)
	if err := os.Symlink(l.s.Src, p); err != nil {
		return err
	}
	uid, gid, _ := e.owner(l.s)
	return e.chown(p, uid, gid)
}

// --- git_clone ---------------------------------------------------------------

type gitCloneStep struct{ s catalog.Step }

func (g gitCloneStep) Plan(*Env) []Action { return []Action{{"clone", g.s.Repo + " → " + g.s.To}} }

func (g gitCloneStep) Check(_ context.Context, e *Env) (bool, error) {
	_, err := os.Stat(e.Path(g.s.To))
	return err == nil, nil
}

func (g gitCloneStep) Apply(ctx context.Context, e *Env) error {
	args := []string{"clone", "-q", "--depth", "1"}
	if g.s.Ref != "" {
		args = append(args, "--branch", g.s.Ref)
	}
	_, _, user := e.owner(g.s)
	_, err := e.run(ctx, Cmd{Name: "git", Args: append(args, g.s.Repo, e.Path(g.s.To)), AsUser: user})
	return err
}

// --- systemctl ---------------------------------------------------------------

type systemctlStep struct{ s catalog.Step }

func (u systemctlStep) action() string {
	if u.s.Action == "" {
		return "enable-now"
	}
	return u.s.Action
}

func (u systemctlStep) Plan(*Env) []Action {
	return []Action{{"unit", strings.TrimSpace(u.action() + " " + u.s.Unit)}}
}

// Follows: a restart or daemon-reload runs only after a change.
func (u systemctlStep) Follows() bool { return u.action() != "enable-now" }

func (u systemctlStep) Check(ctx context.Context, e *Env) (bool, error) {
	if u.Follows() {
		return true, nil
	}
	_, err1 := e.Exec.Run(ctx, Cmd{Name: "systemctl", Args: []string{"is-enabled", "--quiet", u.s.Unit}})
	_, err2 := e.Exec.Run(ctx, Cmd{Name: "systemctl", Args: []string{"is-active", "--quiet", u.s.Unit}})
	return err1 == nil && err2 == nil, nil
}

func (u systemctlStep) Apply(ctx context.Context, e *Env) error {
	var args []string
	switch u.action() {
	case "enable-now":
		args = []string{"enable", "--now", u.s.Unit}
	case "restart":
		args = []string{"restart", u.s.Unit}
	case "daemon-reload":
		args = []string{"daemon-reload"}
	}
	_, err := e.run(ctx, Cmd{Name: "systemctl", Args: args})
	return err
}

// --- sysctl ------------------------------------------------------------------

type sysctlStep struct{ s catalog.Step }

func (s sysctlStep) file() fileStep {
	f := s.s
	f.Owner = "root"
	return fileStep{s: f}
}

func (s sysctlStep) Plan(*Env) []Action { return []Action{{"sysctl", s.s.To}} }

func (s sysctlStep) Check(ctx context.Context, e *Env) (bool, error) { return s.file().Check(ctx, e) }

func (s sysctlStep) Apply(ctx context.Context, e *Env) error {
	if err := s.file().Apply(ctx, e); err != nil {
		return err
	}
	_, err := e.run(ctx, Cmd{Name: "sysctl", Args: []string{"-q", "-p", s.s.To}})
	return err
}

// --- git_config --------------------------------------------------------------

type gitConfigStep struct{ s catalog.Step }

// values are the settings to make, in a stable order, without the ones
// whose answer is empty.
func (g gitConfigStep) values(e *Env) [][2]string {
	name, email := "", ""
	if e.Answers != nil {
		name, email = e.Answers.Git.Name, e.Answers.Git.Email
	}
	r := strings.NewReplacer("{{git_name}}", name, "{{git_email}}", email)
	keys := make([]string, 0, len(g.s.Values))
	for k := range g.s.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out [][2]string
	for _, k := range keys {
		if v := r.Replace(g.s.Values[k]); v != "" {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

func (g gitConfigStep) Plan(e *Env) []Action {
	var out []Action
	for _, kv := range g.values(e) {
		out = append(out, Action{"git", kv[0] + "=" + kv[1]})
	}
	return out
}

func (g gitConfigStep) applies(ctx context.Context, e *Env) bool {
	return g.s.IfTool == "" || root(ctx, e, g.s.IfTool)
}

func (g gitConfigStep) Check(ctx context.Context, e *Env) (bool, error) {
	if !g.applies(ctx, e) {
		return true, nil
	}
	for _, kv := range g.values(e) {
		out, err := e.Exec.Run(ctx, Cmd{Name: "git", Args: []string{"config", "--global", "--get", kv[0]}, AsUser: true})
		if err != nil || strings.TrimSuffix(string(out), "\n") != kv[1] {
			return false, nil
		}
	}
	return true, nil
}

func (g gitConfigStep) Apply(ctx context.Context, e *Env) error {
	if !g.applies(ctx, e) {
		return nil
	}
	for _, kv := range g.values(e) {
		if _, err := e.run(ctx, Cmd{Name: "git", Args: []string{"config", "--global", kv[0], kv[1]}, AsUser: true}); err != nil {
			return err
		}
	}
	return nil
}

// --- user_shell --------------------------------------------------------------

type userShellStep struct{ s catalog.Step }

func (u userShellStep) Plan(e *Env) []Action {
	return []Action{{"shell", e.User + " → " + u.s.Shell}}
}

func (u userShellStep) Check(ctx context.Context, e *Env) (bool, error) {
	out, err := e.Exec.Run(ctx, Cmd{Name: "getent", Args: []string{"passwd", e.User}})
	if err != nil {
		return false, nil
	}
	f := strings.Split(strings.TrimSpace(string(out)), ":")
	return len(f) == 7 && f[6] == u.s.Shell, nil
}

func (u userShellStep) Apply(ctx context.Context, e *Env) error {
	_, err := e.run(ctx, Cmd{Name: "usermod", Args: []string{"-s", u.s.Shell, e.User}})
	return err
}

// --- run ---------------------------------------------------------------------

type runStep struct{ s catalog.Step }

var homeRef = regexp.MustCompile(`^~(/|$)`)

// fill puts the user, their group and home, and the dpkg architecture into
// a run step's command, and {{npm_scope}}, an answer, shell-quoted.
func fill(e *Env, cmd string) string {
	return strings.NewReplacer("{{user}}", e.User, "{{group}}", e.Group, "{{home}}", e.Home, "{{arch}}", e.Arch,
		"{{npm_scope}}", shellQuote(npmScope(e))).Replace(cmd)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (r runStep) Plan(e *Env) []Action { return []Action{{"run", r.cmd(e)}} }

// cmd is the filled-in command; {{sha256}} is the step's pin.
func (r runStep) cmd(e *Env) string {
	return strings.ReplaceAll(fill(e, r.s.Cmd), "{{sha256}}", r.s.SHA256)
}

// Follows: an on_change step runs only after a change.
func (r runStep) Follows() bool { return r.s.OnChange }

func (r runStep) Check(ctx context.Context, e *Env) (bool, error) {
	switch {
	case r.s.Creates != "":
		_, err := os.Stat(e.Path(homeRef.ReplaceAllString(r.s.Creates, "~/")))
		return err == nil, nil
	case r.s.Check == "":
		return true, nil // on_change
	}
	_, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", fill(e, r.s.Check)}, AsUser: r.s.AsUser})
	return err == nil, nil
}

// --- note --------------------------------------------------------------------

type noteStep struct{ s catalog.Step }

func (n noteStep) Plan(e *Env) []Action { return []Action{{"note", fill(e, n.s.Text)}} }

// Always: a note is checked on every run and changes nothing.
func (noteStep) Always() bool { return true }

func (noteStep) Check(context.Context, *Env) (bool, error) { return true, nil }

func (n noteStep) Apply(ctx context.Context, e *Env) error {
	if n.s.When != "" {
		if _, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", fill(e, n.s.When)}}); err != nil {
			return nil
		}
	}
	e.note(fill(e, n.s.Text))
	return nil
}

func (r runStep) Apply(ctx context.Context, e *Env) error {
	_, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c", r.cmd(e)}, AsUser: r.s.AsUser})
	if err == nil && r.s.Text != "" {
		e.note(fill(e, r.s.Text))
	}
	return err
}
