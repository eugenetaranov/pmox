package steps

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func put(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLineAppendsOnce(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	p := e.Path("~/.bashrc")
	put(t, p, "export A=1\n")
	st := step(t, catalog.Step{Type: "line", To: "~/.bashrc", Line: ". ~/x"})
	if check(t, e, st) {
		t.Fatal("Check before Apply = true")
	}
	apply(t, e, st)
	apply(t, e, st)
	// As bash's add_dropin: after a blank line.
	if got := read(t, p); got != "export A=1\n\n. ~/x\n" {
		t.Errorf(".bashrc = %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
}

func TestLineMissingFile(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	st := step(t, catalog.Step{Type: "line", To: "~/.zshrc", Line: ". ~/x", IfExists: true})
	if !check(t, e, st) {
		t.Error("if_exists: a missing file has nothing to do")
	}
	apply(t, e, st)
	if _, err := os.Stat(e.Path("~/.zshrc")); err == nil {
		t.Error("if_exists created the file")
	}
	st = step(t, catalog.Step{Type: "line", To: "~/.bashrc", Line: ". ~/x"})
	apply(t, e, st)
	if got := read(t, e.Path("~/.bashrc")); got != ". ~/x\n" {
		t.Errorf(".bashrc = %q", got)
	}
}

func TestLineMatchReplaces(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	p := e.Path("~/.zshrc")
	put(t, p, "a\nZSH_THEME=\"robbyrussell\"\nb\n")
	st := step(t, catalog.Step{Type: "line", To: "~/.zshrc", Match: "^ZSH_THEME=", Line: `ZSH_THEME="p"`})
	apply(t, e, st)
	if got := read(t, p); got != "a\nZSH_THEME=\"p\"\nb\n" {
		t.Errorf(".zshrc = %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
}

func shellEnv(t *testing.T) *Env {
	e := testEnv(t, &fakeExec{})
	e.Files = os.DirFS("../../bootstrap/conf")
	return e
}

func TestZshrcTheme(t *testing.T) {
	e := shellEnv(t)
	st := step(t, catalog.Step{Type: "go:zshrc"})
	put(t, e.Path("~/.zshrc"), "mine\n")
	apply(t, e, st)
	if got := read(t, e.Path("~/.zshrc")); !strings.Contains(got, `ZSH_THEME="robbyrussell"`) {
		t.Errorf("no default theme in\n%s", got)
	}
	if got := read(t, e.Path("~/.zshrc.orig")); got != "mine\n" {
		t.Errorf(".zshrc.orig = %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
	// Once p10k is cloned, re-rendering keeps its theme; .orig stays.
	if err := os.MkdirAll(e.Path(p10kDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if check(t, e, st) {
		t.Error("Check = true with p10k cloned and the default theme")
	}
	apply(t, e, st)
	if got := read(t, e.Path("~/.zshrc")); !strings.Contains(got, `ZSH_THEME="powerlevel10k/powerlevel10k"`) {
		t.Errorf("no p10k theme in\n%s", got)
	}
	if got := read(t, e.Path("~/.zshrc.orig")); got != "mine\n" {
		t.Errorf(".zshrc.orig = %q after a second render", got)
	}
}

// The steps of zshcfg, shellcfg, p10k and agentkeys all touch .zshrc: once
// each has run, none has more to do (a re-run changes nothing).
func TestZshrcSettles(t *testing.T) {
	e := shellEnv(t)
	zshrc := step(t, catalog.Step{Type: "go:zshrc"})
	dropin := step(t, catalog.Step{Type: "line", To: "~/.zshrc", Line: Dropins[0].Line, IfExists: true})
	theme := step(t, catalog.Step{Type: "line", To: "~/.zshrc", Match: "^ZSH_THEME=", Line: `ZSH_THEME="` + P10kTheme + `"`, IfExists: true})
	apply(t, e, zshrc)
	put(t, e.Path(Dropins[0].File), "# shell\n")
	apply(t, e, dropin)
	if err := os.MkdirAll(e.Path(p10kDir), 0o755); err != nil {
		t.Fatal(err)
	}
	apply(t, e, theme)
	// agentkeys (still bash) appends its line the same way.
	put(t, e.Path(Dropins[1].File), "# agent\n")
	b, _ := os.ReadFile(e.Path("~/.zshrc"))
	put(t, e.Path("~/.zshrc"), string(WithLine(b, Dropins[1].Line, "")))
	for name, st := range map[string]Step{"zshrc": zshrc, "dropin": dropin, "theme": theme} {
		if !check(t, e, st) {
			t.Errorf("%s: Check = false on a settled .zshrc", name)
		}
	}
}

// The Go steps write the same lines as the bash installer still does.
func TestShellMatchesBash(t *testing.T) {
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	val := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + `=(?:'([^']*)'|"([^"]*)")`).FindStringSubmatch(string(b))
		if m == nil {
			t.Fatalf("%s not in the bash installer", name)
		}
		return m[1] + m[2]
	}
	for name, want := range map[string]string{
		"DROPIN_LINE": Dropins[0].Line, "AGENTENV_LINE": Dropins[1].Line,
		"P10K_THEME": P10kTheme, "DEFAULT_ZSH_THEME": DefaultZshTheme,
	} {
		if got := val(name); got != want {
			t.Errorf("%s: bash %q, Go %q", name, got, want)
		}
	}
}

func TestLineTop(t *testing.T) {
	e := testEnv(t, &fakeExec{})
	p := e.Path("~/.bashrc")
	put(t, p, "case $- in *i*) ;; *) return;; esac\n")
	st := step(t, catalog.Step{Type: "line", To: "~/.bashrc", Line: "export PATH=x", Top: true})
	apply(t, e, st)
	apply(t, e, st)
	if got := read(t, p); got != "export PATH=x\ncase $- in *i*) ;; *) return;; esac\n" {
		t.Errorf(".bashrc = %q", got)
	}
}

func TestAptKernel(t *testing.T) {
	x := &fakeExec{}
	e := testEnv(t, x)
	e.Kernel = "6.8.0-45-generic"
	st := step(t, catalog.Step{Type: "apt", Packages: []string{"linux-tools-{{kernel}}"}})
	apply(t, e, st)
	if got := x.commands(); len(got) != 1 || !strings.HasSuffix(got[0], " install linux-tools-6.8.0-45-generic") {
		t.Errorf("ran %q", got)
	}
}

// The local override files are sourced last: drop-ins and line steps go
// before the block that sources ~/.zshrc.local, and each config file
// devbox-setup writes ends by sourcing its local file.
func TestLocalOverridesLast(t *testing.T) {
	e := shellEnv(t)
	put(t, e.Path("~/.config/devbox/shell.sh"), "")
	apply(t, e, step(t, catalog.Step{Type: "go:zshrc"}))
	apply(t, e, step(t, catalog.Step{Type: "line", To: "~/.zshrc", Line: ". ~/.config/devbox/agent-env.sh", IfExists: true}))
	got := read(t, e.Path("~/.zshrc"))
	last := "[[ -r ~/.zshrc.local ]] && source ~/.zshrc.local\n"
	if !strings.HasSuffix(got, last) {
		t.Errorf(".zshrc doesn't end by sourcing .zshrc.local:\n%s", got)
	}
	mark := strings.Index(got, LastMark)
	for _, l := range []string{Dropins[0].Line, ". ~/.config/devbox/agent-env.sh"} {
		if i := strings.Index(got, l); i < 0 || i > mark {
			t.Errorf("%q isn't before the local overrides:\n%s", l, got)
		}
	}
	for f, want := range map[string]string{
		"shell.sh":  "[ -r ~/.config/devbox/local.sh ] && . ~/.config/devbox/local.sh\n",
		"tmux.conf": "source-file -q ~/.tmux.local.conf\n",
		"vimrc":     "if filereadable(expand('~/.vimrc.local'))\n  source ~/.vimrc.local\nendif\n",
	} {
		b, err := fs.ReadFile(e.Files, f)
		if err != nil || !strings.HasSuffix(string(b), want) {
			t.Errorf("%s doesn't end with %q (%v)", f, want, err)
		}
	}
}

func TestWithLineBeforeMark(t *testing.T) {
	b := []byte("a\n\n" + LastMark + "\nsource local\n")
	if got, want := string(WithLine(b, "x", "")), "a\n\nx\n\n"+LastMark+"\nsource local\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := WithLine(b, "source local", ""); string(got) != string(b) {
		t.Errorf("a line already there moved: %q", got)
	}
}
