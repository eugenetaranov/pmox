package wizard

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Form embeds a huh form in a stage. huh's own Run would quit the whole
// program on submit/Ctrl-C; here submission is reported by Update and
// Ctrl-C is left to the shell.
type Form struct {
	f    *huh.Form
	last string // view captured before submit: huh renders "" once done
}

// NewForm themes f and detaches it from program-level quitting.
func NewForm(f *huh.Form) *Form {
	f = f.WithTheme(tui.Theme()).WithShowHelp(true)
	f.SubmitCmd, f.CancelCmd = nil, nil
	return &Form{f: f}
}

// Init starts the form (focus, cursor blink, size probe).
func (w *Form) Init() tea.Cmd { return w.f.Init() }

// Update forwards msg and reports whether the form was just submitted.
func (w *Form) Update(msg tea.Msg) (tea.Cmd, bool) {
	if w.f.State != huh.StateNormal {
		return nil, false
	}
	w.last = w.f.View()
	m, cmd := w.f.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		w.f = f
	}
	return cmd, w.f.State == huh.StateCompleted
}

// View renders the form; after submission it keeps showing the page as
// it was submitted (dimmed by the caller if desired) so the page doesn't
// blank out while the stage validates it.
func (w *Form) View() string {
	if w.f.State == huh.StateCompleted {
		return w.last
	}
	return w.f.View()
}

// Submitted reports whether the form has been completed.
func (w *Form) Submitted() bool { return w.f.State == huh.StateCompleted }
