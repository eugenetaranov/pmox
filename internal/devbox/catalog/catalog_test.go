package catalog

import (
	"os"
	"regexp"
	"sort"
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
		if !seen[it.ID] && !goOnly[it.ID] {
			t.Errorf("%s: in the TOML catalog, not in the bash one", it.ID)
		}
	}
}

// goOnly are items added after the port: the bash installer, the fallback
// until the cut-over, doesn't offer them.
var goOnly = map[string]bool{"sudopw": true, "autoupdate-tools": true, "proxy": true, "corpca": true}

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
		"run no-op":     {item("", `type = "run"`+"\ncmd = \"true\""), "needs creates, check or on_change"},
		"unknown key":   {item(`colour = "red"`, `type = "apt"`+"\npackages = [\"x\"]"), "unknown key item.colour"},
		"bad mode":      {item("", `type = "link"`+"\nsrc = \"/a\"\nto = \"/b\"\nmode = \"644\""), "mode \"644\""},
		"unknown tab":   {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"nope\"\nlabel = \"X\"\n[[item.step]]\ntype = \"legacy\"\n")}}, `unknown tab "nope"`},
		"no steps":      {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"dev\"\nlabel = \"X\"\n")}}, "no steps"},
		"local tab":     {fstest.MapFS{"x.toml": {Data: []byte("[[tab]]\nid = \"t\"\ntitle = \"T\"\n")}}, "can't add tabs"},
		"needs a cycle": {fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"a\"\ntab = \"dev\"\nlabel = \"A\"\nneeds = [\"b\"]\n[[item.step]]\ntype = \"legacy\"\n[[item]]\nid = \"b\"\ntab = \"dev\"\nlabel = \"B\"\nneeds = [\"a\"]\n[[item.step]]\ntype = \"legacy\"\n")}}, "needs/after cycle"},
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

// Every file step's src is one of devbox-setup's files.
func TestSourcesExist(t *testing.T) {
	if _, err := Load(nil, os.DirFS("../../bootstrap/conf")); err != nil {
		t.Fatal(err)
	}
}

// Pinned versions match the bash installer's while it exists.
func TestPinsMatchBash(t *testing.T) {
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	c := load(t, nil)
	for id, name := range map[string]string{"ecc": "ECC_VERSION", "mcpjungle": "MCPJUNGLE_VERSION"} {
		v := regexp.MustCompile(`(?m)^` + name + `=(\S+)`).FindStringSubmatch(string(b))[1]
		n := 0
		for _, s := range c.Item(id).Steps {
			n += strings.Count(s.Cmd+s.Check, v)
		}
		// ecc's check names the version too, so a new pin reinstalls.
		if want := map[string]int{"ecc": 2, "mcpjungle": 1}[id]; n != want {
			t.Errorf("%s: %d step fields use %s=%s, want %d", id, n, name, v, want)
		}
	}
}

// The cred steps' keys and the bash installer's CRED_ROWS agree while both
// exist.
func TestCredKeysMatchBash(t *testing.T) {
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	body := regexp.MustCompile(`(?ms)^CRED_ROWS=\((.*?)^\)`).FindStringSubmatch(string(b))[1]
	bash := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*"([^"]+)"`).FindAllStringSubmatch(body, -1) {
		bash[m[1]] = true
	}
	answer := map[string]string{"": "secret", "npm.scope": "A_NPM_SCOPE", "npm.registry": "A_NPM_REGISTRY"}
	n := 0
	for _, it := range load(t, nil).Items {
		for _, s := range it.Steps {
			for _, k := range s.Keys {
				n++
				row := strings.Join([]string{it.ID, k.Name, answer[k.Answer], k.Prompt, k.How, k.When, k.Default}, "|")
				if !bash[row] {
					t.Errorf("not in bash's CRED_ROWS: %s", row)
				}
			}
		}
	}
	if n != len(bash) {
		t.Errorf("%d cred keys, bash has %d rows", n, len(bash))
	}
}

func TestScope(t *testing.T) {
	c, err := Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"ufw": true, "docker": true, "k3s": true, "ollama": true, "registries": false, "zsh": false, "mcpjungle": false, "go": false} {
		if got := c.Item(id).System(); got != want {
			t.Errorf("%s: System() = %v, want %v", id, got, want)
		}
	}
}

func TestDownloadsPinned(t *testing.T) {
	item := func(step string) fstest.MapFS {
		return fstest.MapFS{"x.toml": {Data: []byte("[[item]]\nid = \"x\"\ntab = \"dev\"\nlabel = \"X\"\n[[item.step]]\n" + step)}}
	}
	for step, want := range map[string]string{
		"type = \"run\"\ncmd = \"curl -fsSL https://x/i.sh | sh\"\ncreates = \"/usr/bin/x\"\n":                                   "pin it",
		"type = \"run\"\ncmd = \"curl -o /tmp/x https://x/x\"\ncreates = \"/x\"\nsha256 = \"" + strings.Repeat("a", 64) + "\"\n": "doesn't check {{sha256}}",
		"type = \"apt_repo\"\nname = \"x\"\nkey_url = \"https://x/key\"\nsource = \"Types: deb\"\n":                              "key_fingerprint",
	} {
		if got := loadErr(t, item(step)); !strings.Contains(got, want) {
			t.Errorf("%q: err = %q, want %q", step, got, want)
		}
	}
	for _, step := range []string{
		"type = \"run\"\ncmd = \"curl -fsSL https://x/i.sh | sh\"\ncreates = \"/x\"\nunverified = \"the vendor's own installer\"\n",
		"type = \"run\"\ncmd = \"curl -o /tmp/x https://x/x && echo '{{sha256}}  /tmp/x' | sha256sum -c\"\ncreates = \"/x\"\nsha256 = \"" + strings.Repeat("a", 64) + "\"\n",
	} {
		load(t, item(step))
	}
}

// The items installed without a pinned checksum, kept explicit: adding one
// is a decision, not an accident.
func TestUnverifiedList(t *testing.T) {
	var got []string
	for _, it := range load(t, nil).Items {
		for _, s := range it.Steps {
			if s.Unverified != "" {
				got = append(got, it.ID)
			}
		}
	}
	sort.Strings(got)
	if want := "k3s krew ollama omp tailscale vimcfg"; strings.Join(got, " ") != want {
		t.Errorf("unverified downloads: %s\nwant %s (update this list when that's intended)", strings.Join(got, " "), want)
	}
}
