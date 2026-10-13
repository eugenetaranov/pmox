package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
	x.reply = func(Cmd) ([]byte, error) { return []byte("E: The repository is not signed"), errors.New("exit status 100") }
	n = 0
	if err := AptUpdate(context.Background(), testEnv(t, x)); err == nil || len(x.ran) == 0 {
		t.Error("other errors fail at once")
	}
}
