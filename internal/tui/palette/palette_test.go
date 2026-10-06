package palette

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func testResolver(path []string) Menu {
	if len(path) == 1 && path[0] == "vm" {
		return Menu{Items: []Item{
			{Key: "list", Desc: "List VMs", Section: "Lifecycle"},
			{Key: "shell", Desc: "Open a shell on a VM", Section: "Access"},
		}}
	}
	return Menu{Items: []Item{
		{Key: "init", Desc: "Set up a Proxmox connection", Section: "Get started"},
		{Key: "list", Desc: "List VMs", Hint: "pmox vm list", Section: "Common"},
		{Key: "vm", Desc: "All VM commands", Section: "Resources", Sub: true},
	}}
}

func press(m *Model, keys ...tea.KeyMsg) {
	for _, k := range keys {
		m.Update(k)
	}
}

var (
	enter = tea.KeyMsg{Type: tea.KeyEnter}
	esc   = tea.KeyMsg{Type: tea.KeyEsc}
	down  = tea.KeyMsg{Type: tea.KeyDown}
)

func typed(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestChooseTopLevel(t *testing.T) {
	m := New(nil, testResolver, Options{Title: "pmox"})
	press(m, down, enter)
	if !slices.Equal(m.Chosen(), []string{"list"}) {
		t.Fatalf("chosen = %v", m.Chosen())
	}
}

func TestGroupOpensInPlaceAndEscGoesBack(t *testing.T) {
	m := New(nil, testResolver, Options{Title: "pmox"})
	press(m, down, down, enter) // vm
	if !slices.Equal(m.Path(), []string{"vm"}) || m.Done() {
		t.Fatalf("path = %v done=%v", m.Path(), m.Done())
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "pmox › vm") || !strings.Contains(v, "esc back") {
		t.Errorf("breadcrumb/back hint missing:\n%s", v)
	}
	press(m, esc)
	if len(m.Path()) != 0 || m.Done() {
		t.Fatalf("esc should go back to the root, path=%v", m.Path())
	}
	press(m, enter) // cursor lands back on "vm"
	press(m, down, enter)
	if !slices.Equal(m.Chosen(), []string{"vm", "shell"}) {
		t.Fatalf("chosen = %v", m.Chosen())
	}
}

func TestFilter(t *testing.T) {
	m := New(nil, testResolver, Options{Title: "pmox"})
	press(m, typed("vm"))
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "vm ") || strings.Contains(v, "GET STARTED") {
		t.Errorf("filtered view:\n%s", v)
	}
	// "vm" matches the vm group by name first, then "list" via its hint.
	press(m, enter)
	if !slices.Equal(m.Path(), []string{"vm"}) {
		t.Fatalf("name-prefix match should rank first; path = %v", m.Path())
	}
	press(m, typed("zzz"))
	if !strings.Contains(ansi.Strip(m.View()), `nothing matches "zzz"`) {
		t.Error("no-match message missing")
	}
	press(m, esc) // clears the filter, stays in the group
	if !slices.Equal(m.Path(), []string{"vm"}) || m.Done() {
		t.Errorf("esc with a filter should only clear it")
	}
}

func TestStartInGroupCannotGoAbove(t *testing.T) {
	m := New([]string{"vm"}, testResolver, Options{Title: "pmox"})
	press(m, esc)
	if !m.Done() || m.Chosen() != nil {
		t.Fatalf("esc at the starting group should quit, done=%v chosen=%v", m.Done(), m.Chosen())
	}
}

func TestCtrlCAborts(t *testing.T) {
	m := New(nil, testResolver, Options{Title: "pmox"})
	press(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.Done() || m.Chosen() != nil {
		t.Fatal("ctrl+c must abort without a choice")
	}
}

func TestViewFitsWidth(t *testing.T) {
	m := New(nil, testResolver, Options{Title: "pmox", Context: "home"})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	for _, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > 50 {
			t.Fatalf("line wider than terminal (%d): %q", w, ansi.Strip(l))
		}
	}
}
