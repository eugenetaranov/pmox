package catalog

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func load(t *testing.T, local fstest.MapFS) *Catalog {
	t.Helper()
	c, err := Load(local, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func loadErr(t *testing.T, local fstest.MapFS) string {
	t.Helper()
	_, err := Load(local, nil)
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	return err.Error()
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	c := load(t, nil)
	if len(c.Tabs) != 9 || c.Tabs[0].ID != "system" || c.Tabs[8].ID != "accounts" {
		t.Errorf("tabs = %+v", c.Tabs)
	}
	if c.Item("zsh") == nil || c.Item("mise") == nil || !c.Item("mise").Hidden() {
		t.Error("zsh and the hidden mise item should load")
	}
	if got := c.Item("terraform").Describe(); got != "mise use -g terraform" {
		t.Errorf("terraform source = %q", got)
	}
}

// While the bash installer exists, the TOML catalog must offer exactly its
// items, with the same tabs, labels and needs.
func TestMatchesBashCatalog(t *testing.T) {
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash catalog gone")
	}
	script := string(b)
	c := load(t, nil)
	seen := map[string]bool{}
	for _, name := range []string{"HIDDEN_ROWS", "CATALOG_ROWS"} {
		body := regexp.MustCompile(`(?ms)^` + name + `=\((.*?)^\)`).FindStringSubmatch(script)[1]
		for _, m := range regexp.MustCompile(`(?m)^\s*"([^"]+)"`).FindAllStringSubmatch(body, -1) {
			f := strings.Split(m[1], "|")
			id := f[0]
			seen[id] = true
			it := c.Item(id)
			if it == nil {
				t.Errorf("%s: in the bash catalog, not in the TOML one", id)
				continue
			}
			if it.Tab != f[1] || it.Label != f[2] || strings.Join(it.Needs, " ") != strings.Join(strings.Fields(f[5]), " ") {
				t.Errorf("%s: TOML has tab %q label %q needs %v; bash has %q %q %q", id, it.Tab, it.Label, it.Needs, f[1], f[2], f[5])
			}
		}
	}
	for _, it := range c.Items {
		if !seen[it.ID] {
			t.Errorf("%s: in the TOML catalog, not in the bash one", it.ID)
		}
	}
}

const teamTab = `
[[item]]
id = "teamtools"
tab = "dev"
label = "Team tools"
[[item.step]]
type = "apt"
packages = ["jq"]
`

func TestLocalItemAdded(t *testing.T) {
	c := load(t, fstest.MapFS{"team.toml": {Data: []byte(teamTab)}})
	it := c.Item("teamtools")
	if it == nil || it.From != LocalDir+"/team.toml" {
		t.Fatalf("local item = %+v", it)
	}
	found := false
	for _, x := range c.TabItems("dev") {
		found = found || x.ID == "teamtools"
	}
	if !found {
		t.Error("teamtools not on the dev tab")
	}
}

func TestLocalItemReplaces(t *testing.T) {
	c := load(t, fstest.MapFS{"x.toml": {Data: []byte(`
[[item]]
id = "tmux"
tab = "shell"
label = "tmux (ours)"
[[item.step]]
type = "apt"
packages = ["tmux", "tmux-plugin-manager"]
`)}})
	it := c.Item("tmux")
	if it.Label != "tmux (ours)" || len(it.Steps[0].Packages) != 2 {
		t.Errorf("tmux not replaced: %+v", it)
	}
	n := 0
	for _, x := range c.Items {
		if x.ID == "tmux" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("tmux listed %d times", n)
	}
}

func TestValidation(t *testing.T) {
	item := func(extra, step string) fstest.MapFS {
		return fstest.MapFS{"bad.toml": {Data: []byte("[[item]]\nid = \"bad\"\ntab = \"dev\"\nlabel = \"Bad\"\n" + extra + "\n[[item.step]]\n" + step + "\n")}}
	}
	cases := map[string]struct {
		fs   fstest.MapFS
		want string
	}{
		"unknown need":  {item(`needs = ["nope"]`, `type = "apt"`+"\npackages = [\"x\"]"), `"bad": needs "nope"`},
		"unknown step":  {item("", `type = "frobnicate"`), `unknown step type "frobnicate"`},
		"run no-op":     {item("", `type = "run"`+"\ncmd = \"true\""), "needs creates or check"},
		"unknown key":   {item(`colour = "red"`, `type = "apt"`+"\npackages = [\"x\"]"), "unknown key item.colour"},
		"bad mode":      {item("", `type = "link"`+"\nsrc = \"/a\"\nto = \"/b\"\nmode = \"644\""), "mode \"644\""},
		"unknown tab":   {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"nope\"\nlabel = \"X\"\n[[item.step]]\ntype = \"legacy\"\n")}}, `unknown tab "nope"`},
		"no steps":      {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"dev\"\nlabel = \"X\"\n")}}, "no steps"},
		"local tab":     {fstest.MapFS{"x.toml": {Data: []byte("[[tab]]\nid = \"t\"\ntitle = \"T\"\n")}}, "can't add tabs"},
		"needs a cycle": {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"a\"\ntab = \"dev\"\nlabel = \"A\"\nneeds = [\"b\"]\n[[item.step]]\ntype = \"legacy\"\n[[item]]\nid = \"b\"\ntab = \"dev\"\nlabel = \"B\"\nneeds = [\"a\"]\n[[item.step]]\ntype = \"legacy\"\n")}}, "needs cycle"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := loadErr(t, tc.fs); !strings.Contains(got, tc.want) {
				t.Errorf("error %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestMissingFileSrc(t *testing.T) {
	local := fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"dev\"\nlabel = \"X\"\n[[item.step]]\ntype = \"file\"\nsrc = \"nope.conf\"\nto = \"~/.nope\"\n")}}
	_, err := Load(local, fstest.MapFS{"tmux.conf": {Data: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), `src "nope.conf" is not in devbox-setup's files`) {
		t.Errorf("err = %v", err)
	}
}
