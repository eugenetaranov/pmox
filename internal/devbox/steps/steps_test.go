package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// fakeExec records commands; reply decides each one's output and error.
type fakeExec struct {
	ran   []Cmd
	reply func(c Cmd) ([]byte, error)
}

func (f *fakeExec) Run(_ context.Context, c Cmd) ([]byte, error) {
	f.ran = append(f.ran, c)
	if f.reply != nil {
		return f.reply(c)
	}
	return nil, nil
}

func (f *fakeExec) commands() []string {
	var out []string
	for _, c := range f.ran {
		out = append(out, c.String())
	}
	return out
}

func testEnv(t *testing.T, x *fakeExec) *Env {
	t.Helper()
	root := t.TempDir()
	return &Env{User: "ada", Home: "/home/ada", Arch: "amd64", Codename: "noble", Exec: x, Root: root,
		Files: fstest.MapFS{
			"tmux.conf":  {Data: []byte("set -g mouse on\n")},
			"hello.tmpl": {Data: []byte("hi {{.user}} at {{.home}}, {{.colour}}\n")},
			"sys.conf":   {Data: []byte("fs.inotify.max_user_watches = 524288\n")},
		}}
}

func step(t *testing.T, s catalog.Step) Step {
	t.Helper()
	st, err := New("x", s)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func check(t *testing.T, e *Env, st Step) bool {
	t.Helper()
	done, err := st.Check(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func apply(t *testing.T, e *Env, st Step) {
	t.Helper()
	if err := st.Apply(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFileKeepsOrigOnce(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	p := e.Path("~/.tmux.conf")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := step(t, catalog.Step{Type: "file", Src: "tmux.conf", To: "~/.tmux.conf", KeepOrig: true})
	if check(t, e, st) {
		t.Fatal("check says done before apply")
	}
	apply(t, e, st)
	if read(t, p) != "set -g mouse on\n" || read(t, p+".orig") != "mine\n" {
		t.Fatal("file or .orig wrong")
	}
	if !check(t, e, st) {
		t.Error("check not done after apply")
	}
	// A later change of ours doesn't overwrite the .orig.
	if err := os.WriteFile(p, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apply(t, e, st)
	if read(t, p+".orig") != "mine\n" {
		t.Error(".orig overwritten")
	}
}

func TestFileIfAbsentAndMode(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	st := step(t, catalog.Step{Type: "file", Src: "tmux.conf", To: "/etc/x.conf", Mode: "0600", IfAbsent: true})
	apply(t, e, st)
	fi, _ := os.Stat(e.Path("/etc/x.conf"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if err := os.WriteFile(e.Path("/etc/x.conf"), []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	apply(t, e, st)
	if read(t, e.Path("/etc/x.conf")) != "theirs" || !check(t, e, st) {
		t.Error("if_absent overwrote an existing file")
	}
}

func TestTemplate(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	st := step(t, catalog.Step{Type: "template", Src: "hello.tmpl", To: "~/hello", Vars: map[string]string{"colour": "teal"}})
	apply(t, e, st)
	if got := read(t, e.Path("~/hello")); got != "hi ada at /home/ada, teal\n" {
		t.Errorf("template = %q", got)
	}
	bad := step(t, catalog.Step{Type: "template", Src: "hello.tmpl", To: "~/hello2"})
	if err := bad.Apply(context.Background(), e); err == nil {
		t.Error("a missing var should fail")
	}
}

func TestAptStep(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "dpkg-query" && c.Args[2] == "jq" {
			return []byte("install ok installed"), nil
		}
		return nil, errors.New("no")
	}}
	e := testEnv(t, x)
	st := step(t, catalog.Step{Type: "apt", Packages: []string{"jq", "ripgrep"}})
	if check(t, e, st) {
		t.Error("ripgrep is missing; check should say not done")
	}
	x.reply = nil
	apply(t, e, st)
	last := x.ran[len(x.ran)-1]
	if last.Name != "apt-get" || !strings.Contains(last.String(), "install jq ripgrep") || !strings.Contains(strings.Join(last.Env, " "), "NEEDRESTART_MODE=l") {
		t.Errorf("apt command = %s %v", last, last.Env)
	}
}

func TestMiseStep(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		return []byte(`{"terraform": [], "pipx:aider-chat": []}`), nil
	}}
	e := testEnv(t, x)
	if !check(t, e, step(t, catalog.Step{Type: "mise", Tools: []string{"terraform@latest", "pipx:aider-chat[uvx_args=--python=3.12]"}})) {
		t.Error("installed tools should check done")
	}
	if check(t, e, step(t, catalog.Step{Type: "mise", Tools: []string{"k9s@latest"}})) {
		t.Error("k9s isn't installed")
	}
	apply(t, e, step(t, catalog.Step{Type: "mise", Tools: []string{"k9s@latest"}}))
	if got := x.commands(); got[len(got)-2] != "(as user) mise use -g --yes k9s@latest" {
		t.Errorf("commands = %v", got)
	}
}

func TestAptRepo(t *testing.T) {
	const fpr = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		return []byte("pub:-:4096:1:8D81803C0EBFCD88:::\nfpr:::::::::" + fpr + ":\n"), nil
	}}
	e := testEnv(t, x)
	e.Download = func(context.Context, string) ([]byte, error) { return []byte("KEY"), nil }
	s := catalog.Step{Type: "apt_repo", Name: "docker", KeyURL: "https://download.docker.com/linux/ubuntu/gpg", KeyFingerprint: fpr,
		Source: "Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: {{codename}}\nArchitectures: {{arch}}\nSigned-By: {{keyring}}\n"}
	st := step(t, s)
	apply(t, e, st)
	if got := read(t, e.Path("/etc/apt/sources.list.d/docker.sources")); !strings.Contains(got, "Suites: noble\nArchitectures: amd64\nSigned-By: /etc/apt/keyrings/docker.asc") {
		t.Errorf("sources = %q", got)
	}
	if !check(t, e, st) {
		t.Error("not done after apply")
	}

	s.Name, s.KeyFingerprint = "other", "0000000000000000000000000000000000000000"
	if err := step(t, s).Apply(context.Background(), e); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("wrong key: err = %v", err)
	}
	if _, err := os.Stat(e.Path("/etc/apt/keyrings/other.asc")); err == nil {
		t.Error("a key with the wrong fingerprint was installed")
	}
}

func TestRunNeedsCreatesOrCheck(t *testing.T) {
	x := &fakeExec{}
	e := testEnv(t, x)
	st := step(t, catalog.Step{Type: "run", Cmd: "touch ~/made", Creates: "~/made", AsUser: true})
	if check(t, e, st) {
		t.Error("done before creates exists")
	}
	if err := os.MkdirAll(e.Path("~"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Path("~/made"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !check(t, e, st) {
		t.Error("creates exists; should be done")
	}
	x.reply = func(Cmd) ([]byte, error) { return nil, errors.New("exit 1") }
	if check(t, e, step(t, catalog.Step{Type: "run", Cmd: "x", Check: "false"})) {
		t.Error("a failing check means not done")
	}
}

func TestSystemctlAndShell(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "getent" {
			return []byte("ada:x:1000:1000::/home/ada:/bin/bash\n"), nil
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	if !check(t, e, step(t, catalog.Step{Type: "systemctl", Unit: "docker"})) {
		t.Error("enabled+active unit should be done")
	}
	restart := step(t, catalog.Step{Type: "systemctl", Unit: "docker", Action: "restart"})
	if f, ok := restart.(Follower); !ok || !f.Follows() {
		t.Error("restart should follow changes")
	}
	if check(t, e, step(t, catalog.Step{Type: "user_shell", Shell: "/usr/bin/zsh"})) {
		t.Error("shell is bash; zsh step not done")
	}
}

func TestLegacy(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	st := step(t, catalog.Step{Type: "legacy"})
	if err := st.Apply(context.Background(), e); err == nil {
		t.Error("no legacy runner: want an error")
	}
	var got string
	e.Legacy = func(_ context.Context, id string) error { got = id; return nil }
	apply(t, e, st)
	if got != "x" {
		t.Errorf("legacy ran %q", got)
	}
}

// Every embedded catalog item has steps this package can build.
func TestCatalogStepsBuild(t *testing.T) {
	c, err := catalog.Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range c.Items {
		for _, s := range it.Steps {
			if _, err := New(it.ID, s); err != nil {
				t.Errorf("%s: %v", it.ID, err)
			}
		}
	}
}

func TestAptUpdateWaitsForTheListsLock(t *testing.T) {
	old := AptRetryDelay
	AptRetryDelay = time.Millisecond
	t.Cleanup(func() { AptRetryDelay = old })
	n := 0
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		n++
		if n < 3 {
			return []byte("E: Could not get lock /var/lib/apt/lists/lock. It is held by process 812 (apt-get)"), errors.New("exit status 100")
		}
		return nil, nil
	}}
	if err := AptUpdate(context.Background(), testEnv(t, x)); err != nil || n != 3 {
		t.Errorf("err %v after %d tries, want success on the 3rd", err, n)
	}
	x.reply = func(Cmd) ([]byte, error) {
		return []byte("E: The repository is not signed"), errors.New("exit status 100")
	}
	n = 0
	if err := AptUpdate(context.Background(), testEnv(t, x)); err == nil || len(x.ran) == 0 {
		t.Error("other errors fail at once")
	}
}

func TestMiseLock(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		return []byte(`{"terraform": [{"version": "1.10.0", "installed": true, "active": true}]}`), nil
	}}
	e := testEnv(t, x)
	st := step(t, catalog.Step{Type: "mise", Tools: []string{"terraform@latest", "pipx:aider-chat[uvx_args=--python=3.12]", "node"}})
	e.Lock = &answers.Lock{Mise: map[string]string{"terraform": "1.9.8", "pipx:aider-chat": "0.80.0"}}
	if got, want := LockedTools(e, []string{"terraform@latest", "pipx:aider-chat[uvx_args=--python=3.12]", "node"}),
		[]string{"terraform@1.9.8", "pipx:aider-chat[uvx_args=--python=3.12]@0.80.0", "node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("LockedTools = %v, want %v", got, want)
	}
	if check(t, e, step(t, catalog.Step{Type: "mise", Tools: []string{"terraform@latest"}})) {
		t.Error("terraform 1.10.0 is installed, the lock wants 1.9.8: not done")
	}
	apply(t, e, st)
	if got := x.commands(); got[len(got)-2] != "(as user) mise use -g --yes terraform@1.9.8 pipx:aider-chat[uvx_args=--python=3.12]@0.80.0 node" {
		t.Errorf("commands = %v", got)
	}
	e.Lock.Mise["terraform"] = "1.10.0"
	if !check(t, e, step(t, catalog.Step{Type: "mise", Tools: []string{"terraform@latest"}})) {
		t.Error("the locked version is installed: done")
	}
}

func TestAptLock(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "apt-cache" {
			if c.Args[2] == "jq=1.7.1-3build1" {
				return []byte("Package: jq\nVersion: 1.7.1-3build1\n"), nil
			}
			return []byte("E: No packages found"), errors.New("exit status 100")
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	e.Lock = &answers.Lock{Apt: map[string]string{"jq": "1.7.1-3build1", "curl": "8.0.0-1"}}
	if err := AptInstall(context.Background(), e, []string{"jq", "curl", "git"}); err != nil {
		t.Fatal(err)
	}
	got := x.commands()
	if last := got[len(got)-1]; !strings.HasSuffix(last, " install jq=1.7.1-3build1 curl git") {
		t.Errorf("install = %q", last)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "curl 8.0.0-1") {
		t.Errorf("notes = %v", notes)
	}
	if p := step(t, catalog.Step{Type: "apt", Packages: []string{"jq", "git"}}).Plan(e); p[0].Target != "jq=1.7.1-3build1 git" {
		t.Errorf("plan = %v", p)
	}
}

func TestVersions(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		switch {
		case c.Name == "mise":
			return []byte(`{"go": [{"version": "1.22.0", "installed": true}, {"version": "1.23.4", "installed": true, "active": true}], "k9s": [{"version": "0.32.0", "installed": false}]}`), nil
		case c.Name == "dpkg-query" && c.Args[2] == "jq":
			return []byte("install ok installed\t1.7.1-3build1"), nil
		}
		return []byte(""), errors.New("exit status 1")
	}}
	l, missing := Versions(context.Background(), testEnv(t, x), []string{"go@latest", "k9s@latest"}, []string{"jq", "nope"})
	if !reflect.DeepEqual(l.Mise, map[string]string{"go": "1.23.4"}) || !reflect.DeepEqual(l.Apt, map[string]string{"jq": "1.7.1-3build1"}) {
		t.Errorf("lock = %+v", l)
	}
	if !reflect.DeepEqual(missing, []string{"k9s", "nope"}) {
		t.Errorf("missing = %v", missing)
	}
}

// A run step's text is a note when the step ran, not when it was done.
func TestRunStepNote(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	st := step(t, catalog.Step{Type: "run", Cmd: "usermod -aG docker {{user}}", Check: "false", Text: "{{user}} joined docker"})
	apply(t, e, st)
	if len(notes) != 1 || notes[0] != "ada joined docker" {
		t.Errorf("notes = %q", notes)
	}
}
