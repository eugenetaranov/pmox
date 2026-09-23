package tui

import (
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// pmox palette — a Claude-style warm coral accent on neutral text.
var (
	Accent = lipgloss.Color("#D97757") // coral
	subtle = lipgloss.Color("#6C6C6C")
	invert = lipgloss.Color("#1A1A1A")
)

// Theme returns the shared pmox huh theme so every prompt/form/picker
// renders with a consistent coral accent.
func Theme() *huh.Theme {
	t := huh.ThemeBase()

	f := &t.Focused
	f.Base = f.Base.BorderForeground(Accent)
	f.Title = f.Title.Foreground(Accent).Bold(true)
	f.SelectSelector = f.SelectSelector.Foreground(Accent)
	f.SelectedOption = f.SelectedOption.Foreground(Accent)
	f.MultiSelectSelector = f.MultiSelectSelector.Foreground(Accent)
	f.FocusedButton = f.FocusedButton.Background(Accent).Foreground(invert)
	f.TextInput.Cursor = f.TextInput.Cursor.Foreground(Accent)
	f.TextInput.Prompt = f.TextInput.Prompt.Foreground(Accent)

	t.Blurred.Title = t.Blurred.Title.Foreground(subtle)
	return t
}

// Steps renders a tab bar of wizard phases with the active one highlighted,
// e.g. Connection › Defaults › Access › Review.
func Steps(active string, steps ...string) string {
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(invert).Background(Accent).Padding(0, 1)
	idleStyle := lipgloss.NewStyle().Foreground(subtle).Padding(0, 1)
	sep := lipgloss.NewStyle().Foreground(subtle).Render("›")

	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		if s == active {
			parts = append(parts, activeStyle.Render(s))
		} else {
			parts = append(parts, idleStyle.Render(s))
		}
	}
	return strings.Join(parts, sep)
}
