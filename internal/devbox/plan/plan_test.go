package plan

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

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

func resolve(t *testing.T, picks ...string) []string {
	t.Helper()
	items, err := Resolve(cat(t), picks, false)
	if err != nil {
		t.Fatal(err)
	}
	return IDs(items)
}

func TestNeedsBeforeAndCatalogOrder(t *testing.T) {
	if got, want := resolve(t, "p10k", "cli"), []string{"cli", "zsh", "zshcfg", "p10k"}; !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

func TestMiseFirst(t *testing.T) {
	got := resolve(t, "terraform", "cli")
	if got[0] != "mise" || !contains(got, "terraform") {
		t.Errorf("plan = %v, want mise first", got)
	}
	items, _ := Resolve(cat(t), []string{"cli"}, true)
	if IDs(items)[0] != "mise" {
		t.Errorf("extra mise tools should pull in mise: %v", IDs(items))
	}
}

// npmrc is on the Dev tools tab but needs node (Languages): it goes after.
func TestCrossTabNeeds(t *testing.T) {
	got := resolve(t, "npmrc")
	if index(got, "node") > index(got, "npmrc") || index(got, "mise") > index(got, "node") {
		t.Errorf("plan = %v, want mise, node, npmrc in that order", got)
	}
}

func TestUnknownPick(t *testing.T) {
	if _, err := Resolve(cat(t), []string{"nope"}, false); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("err = %v", err)
	}
}

// Same plan as the bash installer's resolve(), for a spread of picks.
func TestMatchesBashResolve(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	script := string(b)
	part := func(start, end string) string {
		i := strings.Index(script, start) + 1
		j := strings.Index(script[i:], end)
		return "\n" + script[i:i+j+len(end)]
	}
	src := part("\nCATALOG_ROWS=(", "\n)\n") + part("\nHIDDEN_ROWS=(", "\n)\n") +
		part("\nrow() {", "\n}\n") + part("\nfield() {", "\n") + part("\nhow_of()", "\n") + part("\nneeds_of()", "\n") +
		part("\nresolve() {", "\n}\n") + "RUN_MISE_EXTRA=\nresolve $PICKS\n"
	for _, picks := range [][]string{
		{"p10k", "cli"}, {"fzftab"}, {"terraform", "k8sextra", "docker"}, {"gemini", "ecc"}, {"ruby", "rust"},
		{"gitsign", "registries", "agekey"}, {"mcpjungle", "act", "ufw"},
	} {
		cmd := exec.Command(bash, "-c", src)
		cmd.Env = append(os.Environ(), "PICKS="+strings.Join(picks, " "))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash resolve: %v", err)
		}
		bashSet := strings.Fields(string(out))
		goPlan := resolve(t, picks...)
		if !sameSet(bashSet, goPlan) {
			t.Errorf("%v: bash %v, go %v", picks, bashSet, goPlan)
		}
	}
}

func contains(l []string, s string) bool { return index(l, s) >= 0 }

func index(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	return true
}

// registries goes after the GitHub login when both are installed (ghcr.io
// can use gh's token), and doesn't pull it in.
func TestAfterOrdersWithoutPulling(t *testing.T) {
	c, err := catalog.Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Resolve(c, []string{"registries", "github"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := " " + strings.Join(IDs(items), " ") + " "
	if strings.Index(ids, " github ") > strings.Index(ids, " registries ") {
		t.Errorf("order %s: want github before registries", ids)
	}
	items, _ = Resolve(c, []string{"registries"}, false)
	if strings.Contains(" "+strings.Join(IDs(items), " ")+" ", " github ") {
		t.Errorf("registries pulled in github: %v", IDs(items))
	}
}
