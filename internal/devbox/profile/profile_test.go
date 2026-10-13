package profile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func cat(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const sample = `
picks      = ["essentials", "cli", "zsh", "zshcfg", "p10k", "docker", "go", "k8s"]
mise_extra = ["starship", "terraform@1.9"]
[git]
name  = "Ada Lovelace"
email = "ada@example.com"
[system]
tz = "Europe/Istanbul"
[mcp]
servers = ["github", "context7"]
`

func TestTypoRejected(t *testing.T) {
	_, err := Parse("p.toml", []byte(`pick = ["cli"]`))
	if err == nil || !strings.Contains(err.Error(), "unknown key pick") {
		t.Errorf("err = %v", err)
	}
	if _, err := Parse("p.toml", []byte("[git]\nnmae = \"x\"\n")); err == nil || !strings.Contains(err.Error(), "git.nmae") {
		t.Errorf("err = %v", err)
	}
}

func TestOldAndUnknownIDs(t *testing.T) {
	p, err := Parse("p.toml", []byte(`picks = ["shell", "nope", "mise"]`))
	if err != nil {
		t.Fatal(err)
	}
	a := &answers.Answers{}
	warn := p.Apply(a, cat(t))
	if want := []string{"zsh", "zshcfg", "p10k"}; !reflect.DeepEqual(a.Picks, want) {
		t.Errorf("picks = %v, want %v", a.Picks, want)
	}
	if len(warn) != 2 || !strings.Contains(warn[0], `"nope"`) || !strings.Contains(warn[1], `"mise"`) {
		t.Errorf("warnings = %v", warn)
	}
}

func TestApplyAccumulates(t *testing.T) {
	p, err := Parse("p.toml", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	a := &answers.Answers{Picks: []string{"lazygit"}, Git: answers.Git{Name: "Old"}}
	if w := p.Apply(a, cat(t)); len(w) != 0 {
		t.Errorf("warnings: %v", w)
	}
	if a.Picks[0] != "lazygit" || len(a.Picks) != 9 || a.Git.Name != "Ada Lovelace" || a.TZ != "Europe/Istanbul" || len(a.MCPServers) != 2 {
		t.Errorf("answers = %+v", a)
	}
}

// Exported on one machine, applied on a fresh one: the same answers.
func TestRoundTrip(t *testing.T) {
	yes := true
	src := &answers.Answers{Picks: []string{"cli", "zsh"}, MiseExtra: []string{"starship"}, Git: answers.Git{Name: "Ada", Email: "ada@example.com"},
		TZ: "UTC", MCPServers: []string{"github"}, MCPListen: "127.0.0.1", WireAgents: &yes}
	b := Export(src)
	p, err := Parse("exported", b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	dst := &answers.Answers{}
	p.Apply(dst, cat(t))
	if !Equal(src, dst) || dst.WireAgents == nil || !*dst.WireAgents {
		t.Errorf("round trip:\n%s\n got %+v", b, dst)
	}
	if strings.Contains(string(b), "token") || strings.Contains(string(b), "user") {
		t.Errorf("export carries the user or a secret:\n%s", b)
	}
}

func TestBadListen(t *testing.T) {
	if _, err := Parse("p", []byte("[mcp]\nlisten = \"10.0.0.1\"\n")); err == nil {
		t.Error("want an error for listen 10.0.0.1")
	}
}
