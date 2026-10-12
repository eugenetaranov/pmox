package bootstrap

import (
	"regexp"
	"strings"
	"testing"
)

// catalogRows returns the rows of a bash array NAME=( "a|b|…" … ) in the script.
func catalogRows(t *testing.T, script, name string) [][]string {
	t.Helper()
	start := strings.Index(script, "\n"+name+"=(\n")
	if start < 0 {
		t.Fatalf("%s not found", name)
	}
	body := script[start+len(name)+4:]
	body = body[:strings.Index(body, "\n)\n")]
	var rows [][]string
	for _, m := range regexp.MustCompile(`(?m)^\s*"([^"]*)"`).FindAllStringSubmatch(body, -1) {
		rows = append(rows, strings.Split(m[1], "|"))
	}
	return rows
}

func TestCatalogConsistent(t *testing.T) {
	b, _ := files.ReadFile("devbox-setup")
	script := string(b)
	rows := catalogRows(t, script, "CATALOG_ROWS")
	hidden := catalogRows(t, script, "HIDDEN_ROWS")
	screens := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(\w+)\|[^"]+"`).FindAllStringSubmatch(script[strings.Index(script, "SCREENS=("):], -1) {
		screens[m[1]] = true
		if m[1] == "accounts" {
			break
		}
	}
	ids := map[string]bool{}
	for _, r := range append(rows, hidden...) {
		if ids[r[0]] {
			t.Errorf("duplicate id %s", r[0])
		}
		ids[r[0]] = true
	}
	used := map[string]bool{}
	for _, r := range append(rows, hidden...) {
		id := r[0]
		if len(r) != 7 {
			t.Errorf("%s: %d fields, want 7", id, len(r))
			continue
		}
		if r[1] != "-" && !screens[r[1]] {
			t.Errorf("%s: unknown screen %q", id, r[1])
		}
		used[r[1]] = true
		switch r[3] {
		case "apt", "mise":
			if r[4] == "" {
				t.Errorf("%s: %s row without packages", id, r[3])
			}
		case "fn", "ask":
			// github logs in interactively after the spinner steps (github_login).
			if id != "github" && !strings.Contains(script, "\ninstall_"+id+"() {") {
				t.Errorf("%s: no install_%s function", id, id)
			}
			if r[6] == "" {
				t.Errorf("%s: %s row without a source description", id, r[3])
			}
		default:
			t.Errorf("%s: unknown how %q", id, r[3])
		}
		for _, n := range strings.Fields(r[5]) {
			if !ids[n] {
				t.Errorf("%s: needs unknown id %s", id, n)
			}
		}
	}
	for s := range screens {
		if !used[s] {
			t.Errorf("screen %s has no items", s)
		}
	}
}

// zsh is three items: the shell, its config, and the prompt, each pulling
// in the one before; saved answers with the old "shell" item get all three.
func TestZshItems(t *testing.T) {
	b, _ := files.ReadFile("devbox-setup")
	script := string(b)
	needs, order, labels := map[string]string{}, map[string]int{}, map[string]string{}
	for n, r := range catalogRows(t, script, "CATALOG_ROWS") {
		needs[r[0]], order[r[0]], labels[r[0]] = r[5], n, r[2]
	}
	if _, ok := needs["shell"]; ok {
		t.Error(`the old "shell" item is still in the catalog`)
	}
	for id, want := range map[string]string{"zsh": "", "zshcfg": "zsh", "p10k": "zshcfg", "fzftab": "zshcfg cli"} {
		if got, ok := needs[id]; !ok || got != want {
			t.Errorf("%s: needs %q, want %q (present %v)", id, got, want, ok)
		}
	}
	seq := []string{"zsh", "zshcfg", "p10k", "fzftab"}
	for i := 1; i < len(seq); i++ {
		if order[seq[i-1]] >= order[seq[i]] {
			t.Errorf("%s must come before %s in the catalog", seq[i-1], seq[i])
		}
	}
	for _, id := range []string{"zsh", "zshcfg", "p10k", "fzftab", "shellcfg"} {
		if !strings.Contains(strings.ToLower(labels[id]), "zsh") {
			t.Errorf("%s: label %q must mention zsh so searching zsh finds it", id, labels[id])
		}
	}

	vals := loadAnswers(t, writeLegacy(t, "A_PICKS='cli shell fzftab'\n"))
	if got := vals["A_PICKS"]; got != "cli zsh zshcfg p10k fzftab" {
		t.Errorf("migrated picks = %q, want %q", got, "cli zsh zshcfg p10k fzftab")
	}
}
