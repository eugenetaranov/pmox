package ui

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"sort"
	"testing"

	"github.com/eugenetaranov/pmox/internal/bootstrap"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// everything is the picker's search set: the catalog and mise's registry.
func everything(t *testing.T) []*Entry {
	t.Helper()
	c, err := catalog.Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Dedupe(append(CatalogEntries(c), LoadMise(string(bootstrap.MiseRegistry()))...))
}

func ids(es []*Entry, n int) []string {
	var out []string
	for i, e := range es {
		if i == n {
			break
		}
		out = append(out, e.ID)
	}
	return out
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// The cases of picker_test.py, so the Go ranking matches the bash picker's.
func TestSearch(t *testing.T) {
	items := everything(t)
	top := ids(Search(items, "zsh"), 5)
	got := append([]string{}, top...)
	sort.Strings(got)
	if want := []string{"fzftab", "p10k", "shellcfg", "zsh", "zshcfg"}; !reflect.DeepEqual(got, want) || top[0] != "zsh" {
		t.Errorf("zsh top 5 = %v", top)
	}
	res := Search(items, "zsh")
	firstMise := -1
	for i, e := range res {
		if e.Tab == MiseTab {
			firstMise = i
			break
		}
	}
	if firstMise < 0 || !has(ids(res, firstMise), "zsh") {
		t.Errorf("catalog items should come before mise tools: %v", ids(res, 10))
	}
	cases := []struct {
		q    string
		n    int
		want string
	}{
		{"k9s", 1, "k8s"}, {"find", 40, "mise:fd"}, {"starship", 1, "mise:starship"},
		{"strshp", 3, "mise:starship"}, {"lzgt", 5, "lazygit"},
	}
	for _, c := range cases {
		if !has(ids(Search(items, c.q), c.n), c.want) {
			t.Errorf("%q: %s not in the top %d: %v", c.q, c.want, c.n, ids(Search(items, c.q), c.n))
		}
	}
	if n := len(Search(items, "starsh")); n >= 10 {
		t.Errorf("descriptions aren't fuzzy-matched: starsh gave %d results", n)
	}
	if n := len(Search(items, "qqqqzzzz")); n != 0 {
		t.Errorf("no match expected, got %d", n)
	}
}

func TestNarrow(t *testing.T) {
	if got := Narrow(":cherry_blossom: A fuzzy finder 🚀 for おはよう you"); got != "A fuzzy finder for you" {
		t.Errorf("Narrow = %q", got)
	}
	if got := Narrow("Kubernetes — ⎈ helm…"); got != "Kubernetes — ⎈ helm…" {
		t.Errorf("Narrow = %q", got)
	}
}

func TestDedupe(t *testing.T) {
	names := map[string]bool{}
	for _, e := range everything(t) {
		names[e.ID] = true
	}
	for _, covered := range []string{"terraform", "k9s", "kubectl", "glances", "helmfile", "aider-chat", "neovim"} {
		if names["mise:"+covered] {
			t.Errorf("mise:%s should be hidden (a catalog item installs it)", covered)
		}
	}
	if !names["mise:starship"] {
		t.Error("mise:starship missing")
	}
}

func TestMiseTools(t *testing.T) {
	cases := map[string][]string{
		"mise use -g uv pipx:aider-chat[uvx_args=--python=3.12] aqua:block/goose": {"aider-chat", "goose", "uv"},
		"apt prometheus-node-exporter · mise pipx:glances":                       {"glances"},
		"apt install mise": nil,
	}
	for src, want := range cases {
		var got []string
		for t := range MiseTools(src) {
			got = append(got, t)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MiseTools(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestLoadMise(t *testing.T) {
	got := LoadMise("op\t1password\tPassword manager. Second\tsentence.\n\tskipped\tx\nbare\t\t\n")
	if len(got) != 2 || got[0].ID != "mise:op" || got[0].Label != "op — Password manager" || !reflect.DeepEqual(got[0].Names, []string{"op", "1password"}) || got[1].Label != "bare" {
		t.Errorf("LoadMise = %+v %+v", got[0], got)
	}
}

// Same top results as picker.py, query by query, while it exists.
func TestSearchMatchesPickerPy(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	queries := []string{"zsh", "k8s", "doc", "py", "terra", "ai", "git", "node", "rust", "lzgt", "mcp", "sql", "fd", "kube", "cloud"}
	script := `
import json, sys
sys.path.insert(0, "../../bootstrap")
import picker_test, picker
items = picker_test.everything()
print(json.dumps({q: [i["id"] for i in picker.search(items, q)[:10]] for q in sys.argv[1:]}))
`
	cmd := exec.Command("python3", append([]string{"-c", script}, queries...)...)
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("picker.py: %v", err)
	}
	var want map[string][]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	items := everything(t)
	for _, q := range queries {
		if got := ids(Search(items, q), 10); !reflect.DeepEqual(got, want[q]) {
			t.Errorf("%q:\n go %v\n py %v", q, got, want[q])
		}
	}
}
