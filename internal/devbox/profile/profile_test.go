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
	_, warn := p.Apply(a, cat(t))
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
	if _, w := p.Apply(a, cat(t)); len(w) != 0 {
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

func TestSecretsRefused(t *testing.T) {
	for _, src := range []string{
		"picks = []\n[creds]\nANTHROPIC_API_KEY = \"sk-1\"\n",
		"picks = []\ngithub_token = \"ghp\"\n",
		"picks = []\n[mcp]\nservers = []\n[mcp.github]\nGITHUB_PERSONAL_ACCESS_TOKEN = \"x\"\n",
		"picks = []\n[tailscale]\nauthkey = \"tskey\"\n",
	} {
		_, err := Parse("p.toml", []byte(src))
		if err == nil || !strings.Contains(err.Error(), "never hold secrets") {
			t.Errorf("%q: err = %v", src, err)
		}
	}
	if _, err := Parse("p.toml", []byte("picks = []\ncolour = \"red\"\n")); err == nil || !strings.Contains(err.Error(), "unknown key colour") {
		t.Errorf("plain unknown key: %v", err)
	}
}

func apply(t *testing.T, a *answers.Answers, src string) []string {
	t.Helper()
	p, err := Parse("p.toml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	dropped, warn := p.Apply(a, cat(t))
	if len(warn) > 0 {
		t.Errorf("warnings: %v", warn)
	}
	return dropped
}

// A named profile applied again replaces its layer; hand picks and other
// layers' picks stay.
func TestNamedLayerReplaces(t *testing.T) {
	a := &answers.Answers{}
	apply(t, a, `picks = ["lazygit", "go"]`) // unnamed: like picks made by hand
	apply(t, a, `name = "team"
picks = ["docker", "k8s", "go", "terraform"]`)
	apply(t, a, `name = "infra"
picks = ["terraform"]`)
	a.Pick([]string{"docker"}) // by hand, after the team listed it
	dropped := apply(t, a, `name = "team"
picks = ["cli"]`)
	if want := []string{"k8s"}; !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
	if want := []string{"lazygit", "go", "docker", "terraform", "cli"}; !reflect.DeepEqual(a.Picks, want) {
		t.Errorf("picks = %v, want %v", a.Picks, want)
	}
	if want := []string{"cli"}; !reflect.DeepEqual(a.Layers["team"], want) {
		t.Errorf("team layer = %v", a.Layers["team"])
	}
	// The same profile again drops nothing.
	if d := apply(t, a, "name = \"team\"\npicks = [\"cli\"]\n"); len(d) != 0 {
		t.Errorf("re-apply dropped %v", d)
	}
}

func TestUserAndName(t *testing.T) {
	a := &answers.Answers{User: "ubuntu"}
	apply(t, a, `user = "ada"`)
	if a.User != "ada" {
		t.Errorf("user = %q", a.User)
	}
	for _, src := range []string{`user = "Ada Lovelace"`, `user = "root;rm"`, `name = "a b"`, `name = ""
user = "-x"`} {
		if _, err := Parse("p.toml", []byte(src)); err == nil {
			t.Errorf("%s: want an error", src)
		}
	}
}
