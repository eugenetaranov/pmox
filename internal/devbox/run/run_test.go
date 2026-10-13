package run

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

type fakeExec struct {
	ran       []string
	installed map[string]bool
	badPkg    string
	miseFail  bool
}

func (f *fakeExec) Run(_ context.Context, c steps.Cmd) ([]byte, error) {
	f.ran = append(f.ran, c.String())
	switch c.Name {
	case "dpkg-query":
		if f.installed[c.Args[2]] {
			return []byte("install ok installed"), nil
		}
		return nil, errors.New("not installed")
	case "apt-get":
		if c.Args[len(c.Args)-1] == "update" {
			return nil, nil
		}
		pkgs := c.Args[indexOf(c.Args, "install")+1:]
		for _, p := range pkgs {
			if p == f.badPkg {
				return []byte("E: Unable to locate package " + p), errors.New("exit status 100")
			}
		}
		for _, p := range pkgs {
			f.installed[p] = true
		}
	case "mise":
		if f.miseFail {
			return []byte("mise: boom"), errors.New("exit status 1")
		}
		if c.Args[0] == "ls" {
			return []byte("{}"), nil
		}
	}
	return nil, nil
}

func indexOf(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func item(id string, needs []string, s ...catalog.Step) *catalog.Item {
	return &catalog.Item{ID: id, Tab: "dev", Label: id, Needs: needs, Steps: s}
}

func aptItem(id string, pkgs ...string) *catalog.Item {
	return item(id, nil, catalog.Step{Type: "apt", Packages: pkgs})
}

func runner(t *testing.T, x *fakeExec) (*Runner, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	e := &steps.Env{User: "ada", Home: "/home/ada", Exec: x, Root: t.TempDir(),
		Files: fstest.MapFS{"a.conf": {Data: []byte("a\n")}}}
	return &Runner{Env: e, Progress: Plain{&out}, TailLines: 5}, &out
}

func TestOneAptTransaction(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{"jq": true}}
	r, _ := runner(t, x)
	res := r.Run(context.Background(), []*catalog.Item{aptItem("a", "jq", "ripgrep"), aptItem("b", "tmux")}, nil)
	if !res.OK() {
		t.Fatalf("failed: %v", res.Errors)
	}
	var installs []string
	for _, c := range x.ran {
		if strings.Contains(c, " install ") {
			installs = append(installs, c)
		}
	}
	if len(installs) != 1 || !strings.HasSuffix(installs[0], "install ripgrep tmux") {
		t.Errorf("installs = %v, want one with ripgrep tmux", installs)
	}
}

func TestBadPackageFailsOnlyItsItem(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{}, badPkg: "nope"}
	r, out := runner(t, x)
	items := []*catalog.Item{aptItem("good", "tmux"), aptItem("bad", "nope"), item("after", []string{"bad"}, catalog.Step{Type: "file", Src: "a.conf", To: "/etc/a.conf"})}
	res := r.Run(context.Background(), items, nil)
	if strings.Join(res.Failed, " ") != "bad" || strings.Join(res.Skipped, " ") != "after" {
		t.Errorf("failed %v skipped %v", res.Failed, res.Skipped)
	}
	if !x.installed["tmux"] {
		t.Error("tmux not installed after the retry")
	}
	if !strings.Contains(res.Errors["bad"], "Unable to locate package nope") {
		t.Errorf("bad's error lacks the output tail: %q", res.Errors["bad"])
	}
	if !strings.Contains(out.String(), "after (skipped: bad failed)") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestMiseFailureSkipsMiseItems(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{}, miseFail: true}
	r, _ := runner(t, x)
	mise := item("mise", nil, catalog.Step{Type: "mise", Tools: []string{"usage"}})
	mise.Tab = catalog.HiddenTab
	tf := item("terraform", nil, catalog.Step{Type: "mise", Tools: []string{"terraform@latest"}})
	res := r.Run(context.Background(), []*catalog.Item{mise, tf}, []string{"starship"})
	if strings.Join(res.Failed, " ") != "mise" || strings.Join(res.Skipped, " ") != "terraform mise-extra" {
		t.Errorf("failed %v skipped %v", res.Failed, res.Skipped)
	}
}

func TestRerunIsNoOp(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{}}
	r, _ := runner(t, x)
	items := []*catalog.Item{
		aptItem("a", "tmux"),
		item("conf", nil, catalog.Step{Type: "file", Src: "a.conf", To: "/etc/a.conf"}, catalog.Step{Type: "systemctl", Unit: "x", Action: "restart"}),
	}
	if res := r.Run(context.Background(), items, nil); len(res.Changed) != 2 {
		t.Fatalf("first run changed %v", res.Changed)
	}
	x.ran = nil
	res := r.Run(context.Background(), items, nil)
	if len(res.Changed) != 0 || len(res.Unchanged) != 2 {
		t.Errorf("second run changed %v unchanged %v", res.Changed, res.Unchanged)
	}
	for _, c := range x.ran {
		if strings.Contains(c, "restart") || strings.Contains(c, " install ") {
			t.Errorf("second run ran %q", c)
		}
	}
}

func TestDryRunAndVerify(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{"tmux": true}}
	r, _ := runner(t, x)
	items := []*catalog.Item{
		aptItem("tmux", "tmux"),
		item("tmuxcfg", nil, catalog.Step{Type: "file", Src: "a.conf", To: "~/.tmux.conf"}),
		item("cli", nil, catalog.Step{Type: "legacy"}),
	}
	var buf bytes.Buffer
	if err := DryRun(&buf, r.Env, items, []string{"starship"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apt    tmux", "file   ~/.tmux.conf", "legacy devbox-setup.sh --only cli", "mise   starship"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("dry run missing %q:\n%s", want, buf.String())
		}
	}
	got := map[string]string{}
	for _, s := range Verify(context.Background(), r.Env, items) {
		got[s.Item.ID] = s.State
	}
	if got["tmux"] != "ok" || got["tmuxcfg"] != "missing" || got["cli"] != "unchecked" {
		t.Errorf("verify = %v", got)
	}
	for _, c := range x.ran {
		if strings.Contains(c, "install") {
			t.Errorf("dry run / verify ran %q", c)
		}
	}
}

func TestSummaryGroups(t *testing.T) {
	var buf bytes.Buffer
	items := []*catalog.Item{
		item("terraform", nil, catalog.Step{Type: "mise", Tools: []string{"terraform@latest"}}),
		aptItem("tmux", "tmux"),
		{ID: "cli", Label: "CLI upgrades", Source: "apt install ripgrep fd-find", Steps: []catalog.Step{{Type: "legacy"}}},
		{ID: "docker", Label: "Docker", Source: "apt docker-ce (download.docker.com repo)", Steps: []catalog.Step{{Type: "legacy"}}},
	}
	Summary(&buf, items, []string{"starship"}, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	s := buf.String()
	mise, apt, other := strings.Index(s, "with mise"), strings.Index(s, "with apt"), strings.Index(s, "Vendor repos")
	if mise < 0 || apt < mise || other < apt || !strings.Contains(s, "(updated 2026-10-13)") {
		t.Fatalf("groups out of order:\n%s", s)
	}
	cli := strings.Index(s, "CLI upgrades")
	if cli < apt || cli > other || strings.Index(s, "Docker") < other {
		t.Errorf("cli belongs with apt, docker with vendor repos:\n%s", s)
	}
}

type alwaysStep struct {
	ran *int
	err error
}

func (alwaysStep) Plan(*steps.Env) []steps.Action                 { return nil }
func (alwaysStep) Check(context.Context, *steps.Env) (bool, error) { return true, nil }
func (a alwaysStep) Apply(context.Context, *steps.Env) error       { *a.ran++; return a.err }
func (alwaysStep) Always() bool                                    { return true }

// An Always step runs on every run without counting as a change, and its
// failure is a note, not a failed item.
func TestAlwaysStep(t *testing.T) {
	ran := 0
	var err error
	steps.RegisterGo("test_always", func(catalog.Step) steps.Step { return alwaysStep{&ran, err} })
	r, _ := runner(t, &fakeExec{installed: map[string]bool{}})
	var notes []string
	r.Env.Note = func(s string) { notes = append(notes, s) }
	it := item("sync", nil, catalog.Step{Type: "go:test_always"})
	res := r.Run(context.Background(), []*catalog.Item{it}, nil)
	if ran != 1 || strings.Join(res.Unchanged, " ") != "sync" {
		t.Errorf("ran %d, unchanged %v", ran, res.Unchanged)
	}
	err = errors.New("gateway down")
	res = r.Run(context.Background(), []*catalog.Item{it}, nil)
	if !res.OK() || len(notes) != 1 || !strings.Contains(notes[0], "gateway down") {
		t.Errorf("failed %v, notes %q", res.Failed, notes)
	}
	if st := Verify(context.Background(), r.Env, []*catalog.Item{it}); st[0].State != "ok" {
		t.Errorf("verify = %+v", st[0])
	}
}

type preStep struct{ x *fakeExec }

func (preStep) Plan(*steps.Env) []steps.Action                 { return nil }
func (preStep) Check(context.Context, *steps.Env) (bool, error) { return false, nil }
func (p preStep) Apply(context.Context, *steps.Env) error {
	p.x.ran = append(p.x.ran, "PRE")
	return nil
}
func (preStep) BeforeApt() bool { return true }

// A BeforeApt step (docker's daemon.json) lands before the batch installs
// the packages that read it.
func TestBeforeAptOrder(t *testing.T) {
	x := &fakeExec{installed: map[string]bool{}}
	steps.RegisterGo("test_pre", func(catalog.Step) steps.Step { return preStep{x} })
	r, _ := runner(t, x)
	it := item("docker", nil, catalog.Step{Type: "go:test_pre"}, catalog.Step{Type: "apt", Packages: []string{"docker-ce"}})
	res := r.Run(context.Background(), []*catalog.Item{it}, nil)
	if !res.OK() || strings.Join(res.Changed, " ") != "docker" {
		t.Fatalf("failed %v changed %v", res.Errors, res.Changed)
	}
	pre, inst := indexOf(x.ran, "PRE"), -1
	for i, c := range x.ran {
		if strings.Contains(c, " install docker-ce") {
			inst = i
		}
	}
	if pre < 0 || inst < 0 || pre > inst {
		t.Errorf("ran %q: want PRE before the install", x.ran)
	}
}
