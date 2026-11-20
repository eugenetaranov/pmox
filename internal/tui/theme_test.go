package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// SetColor(false) strips color from everything rendered through
// lipgloss — pickers, the palette, wizards — not only from callers that
// ask ColorEnabled.
func TestSetColorOffStripsStyles(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() {
		colorOff = false
		lipgloss.SetColorProfile(prev)
	})
	lipgloss.SetColorProfile(termenv.TrueColor)
	if got := Steps("Review", "Connection", "Review"); !strings.Contains(got, "\x1b[") {
		t.Fatalf("precondition: expected styled output, got %q", got)
	}

	SetColor(false)
	if ColorEnabled() {
		t.Error("ColorEnabled should be false")
	}
	for name, got := range map[string]string{
		"steps": Steps("Review", "Connection", "Review"),
		"muted": Muted("clean"),
		"warn":  Warnf("careful"),
		"style": lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("x"),
	} {
		if strings.Contains(got, "\x1b[") {
			t.Errorf("%s still carries escape sequences: %q", name, got)
		}
	}
}
