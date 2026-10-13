package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestPicker(t *testing.T) *Picker {
	t.Helper()
	tabs := []Tab{{"system", "System"}, {"shell", "Shell & CLI"}, {"empty", "Empty"}}
	entries := []*Entry{
		{Tab: "system", ID: "docker", Label: "Docker Engine + Compose", Source: "apt docker-ce"},
		{Tab: "shell", ID: "zsh", Label: "zsh (login shell)", Source: "apt zsh"},
		{Tab: "shell", ID: "tmux", Label: "tmux", Source: "apt install tmux"},
		{Tab: MiseTab, ID: "mise:starship", Label: "starship — prompt", Source: "mise use -g starship", Names: []string{"starship"}},
	}
	p := NewPicker("devbox-setup", tabs, entries, true)
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return p
}

func press(p *Picker, keys ...any) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var m tea.KeyMsg
		switch v := k.(type) {
		case tea.KeyType:
			m = tea.KeyMsg{Type: v}
		case string:
			m = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(v)}
		}
		_, cmd = p.Update(m)
	}
	return cmd
}

func TestPickerTabsAndToggle(t *testing.T) {
	p := newTestPicker(t)
	if len(p.tabs) != 3 || p.tabs[2].ID != reviewTab {
		t.Fatalf("tabs = %v (empty tabs dropped, Review last)", p.tabs)
	}
	press(p, tea.KeySpace) // docker
	press(p, "2", tea.KeyDown, tea.KeySpace)
	if got := strings.Join(p.Selected(), " "); got != "docker tmux" {
		t.Errorf("selected = %q", got)
	}
	press(p, tea.KeyEnter)
	if p.view() != reviewTab || len(p.items(reviewTab)) != 2 {
		t.Errorf("enter on the last category should go to Review: %s", p.view())
	}
	if cmd := press(p, tea.KeyEnter); cmd == nil || !p.Confirmed() {
		t.Error("enter on Review confirms")
	}
}

func TestPickerSearch(t *testing.T) {
	p := newTestPicker(t)
	press(p, "s", "t", "a", "r")
	if p.view() != searchTab || len(p.results) == 0 || p.results[0].ID != "mise:starship" {
		t.Fatalf("search results = %v", ids(p.results, 5))
	}
	press(p, "2") // a digit is part of the query while searching
	if p.query != "star2" {
		t.Errorf("query = %q", p.query)
	}
	press(p, tea.KeyBackspace, tea.KeySpace)
	if !p.entries[3].On {
		t.Error("space should tick the search result")
	}
	if !strings.Contains(p.View(), "Search: ") {
		t.Error("search line not drawn")
	}
	press(p, tea.KeyEsc)
	if p.query != "" {
		t.Error("esc clears the search")
	}
	if cmd := press(p, tea.KeyEsc); cmd == nil || p.Confirmed() {
		t.Error("esc on an empty search quits")
	}
}

func TestPickerEnterOnSearchGoesToReview(t *testing.T) {
	p := newTestPicker(t)
	press(p, "z", "s", "h", tea.KeySpace, tea.KeyEnter)
	if p.view() != reviewTab || p.query != "" {
		t.Errorf("view %s query %q", p.view(), p.query)
	}
}

func TestPickerView(t *testing.T) {
	p := newTestPicker(t)
	press(p, tea.KeySpace)
	v := p.View()
	for _, want := range []string{"devbox-setup", "1 selected", "System (1)", "Docker Engine + Compose", "apt docker-ce", "[x]", "esc quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if n := strings.Count(v, "\n"); n > 30 {
		t.Errorf("view is %d lines, taller than the terminal", n)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("Kubernetes CLIs (kubectl, kubectx, helm 3, k9s)", 16)
	for _, l := range got {
		if len(l) > 16 {
			t.Errorf("line %q wider than 16", l)
		}
	}
	if strings.Join(got, " ") != "Kubernetes CLIs (kubectl, kubectx, helm 3, k9s)" {
		t.Errorf("wrap lost words: %q", got)
	}
}
