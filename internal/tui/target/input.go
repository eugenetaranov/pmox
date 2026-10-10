// Package target provides the interactive fields commands use to ask for
// a remote "<vm>:<path>" target and a local path (openspec/specs/
// remote-target-input): a VM list under the input, greyed suggestions,
// Tab completion and free editing of one value.
package target

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eugenetaranov/pmox/internal/tui"
)

var (
	accentStyle = lipgloss.NewStyle().Foreground(tui.Accent).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5484D"))
	cursorStyle = lipgloss.NewStyle().Reverse(true)
)

// input is a single-line editor: the runes and a cursor position.
type input struct {
	r []rune
	c int
}

func (in *input) set(s string) { in.r, in.c = []rune(s), len([]rune(s)) }
func (in *input) str() string  { return string(in.r) }
func (in *input) atEnd() bool  { return in.c == len(in.r) }

// edit applies an editing key and reports whether the value changed.
// Keys it doesn't know are ignored.
func (in *input) edit(k tea.KeyMsg) (changed bool) {
	switch k.Type {
	case tea.KeyRunes, tea.KeySpace:
		ins := k.Runes
		if k.Type == tea.KeySpace {
			ins = []rune{' '}
		}
		in.r = append(in.r[:in.c], append(append([]rune{}, ins...), in.r[in.c:]...)...)
		in.c += len(ins)
		return true
	case tea.KeyBackspace:
		if in.c == 0 {
			return false
		}
		in.r = append(in.r[:in.c-1], in.r[in.c:]...)
		in.c--
		return true
	case tea.KeyDelete:
		if in.c == len(in.r) {
			return false
		}
		in.r = append(in.r[:in.c], in.r[in.c+1:]...)
		return true
	case tea.KeyCtrlW: // back to the previous separator
		if in.c == 0 {
			return false
		}
		i := in.c - 1
		for i > 0 && strings.ContainsRune("/: ", in.r[i]) {
			i--
		}
		for i > 0 && !strings.ContainsRune("/: ", in.r[i-1]) {
			i--
		}
		in.r = append(in.r[:i], in.r[in.c:]...)
		in.c = i
		return true
	case tea.KeyCtrlU:
		if in.c == 0 {
			return false
		}
		in.r = in.r[in.c:]
		in.c = 0
		return true
	case tea.KeyCtrlK:
		if in.c == len(in.r) {
			return false
		}
		in.r = in.r[:in.c]
		return true
	case tea.KeyLeft:
		if in.c > 0 {
			in.c--
		}
	case tea.KeyRight:
		if in.c < len(in.r) {
			in.c++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		in.c = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		in.c = len(in.r)
	}
	return false
}

// view renders the value with a block cursor and ghost (greyed) text
// after it. dimTo dims the first dimTo runes (a pre-filled VM part).
func (in *input) view(ghost string, dimTo int) string {
	var b strings.Builder
	for i, r := range in.r {
		s := string(r)
		switch {
		case i == in.c:
			b.WriteString(cursorStyle.Render(s))
		case i < dimTo:
			b.WriteString(dimStyle.Render(s))
		default:
			b.WriteString(s)
		}
	}
	if in.atEnd() {
		g := []rune(ghost)
		if len(g) == 0 {
			b.WriteString(cursorStyle.Render(" "))
		} else {
			b.WriteString(cursorStyle.Render(dimStyle.Render(string(g[0]))))
			b.WriteString(dimStyle.Render(string(g[1:])))
		}
	}
	return accentStyle.Render("> ") + b.String()
}

// longestCommonPrefix of ss ("" for none).
func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}
