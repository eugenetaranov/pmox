package wizard

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Dialog is a modal yes/no question shown under the page. While it is
// open it receives every key; Esc picks Safe.
type Dialog struct {
	Title string
	Body  string // optional pre-rendered detail (fingerprints, paths, …)
	// Affirmative/Negative label the buttons (default "Yes"/"No").
	Affirmative, Negative string
	// Default is the initially focused answer.
	Default bool
	// Safe is the answer Esc picks — the choice that writes/trusts
	// nothing. It is usually the same as Default.
	Safe bool
	// OnResult turns the answer into the stage's next command.
	OnResult func(yes bool) tea.Cmd

	value bool
	form  *huh.Form
}

func (d *Dialog) safe() bool { return d.Safe }

func (d *Dialog) init() tea.Cmd {
	d.value = d.Default
	yes, no := d.Affirmative, d.Negative
	if yes == "" {
		yes = "Yes"
	}
	if no == "" {
		no = "No"
	}
	d.form = huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(d.Title).Affirmative(yes).Negative(no).Value(&d.value),
	)).WithTheme(tui.Theme()).WithShowHelp(false)
	d.form.SubmitCmd, d.form.CancelCmd = nil, nil
	return d.form.Init()
}

// update forwards msg to the dialog's form and reports whether the user
// has answered (and with what).
func (d *Dialog) update(msg tea.Msg) (cmd tea.Cmd, decided, value bool) {
	f, cmd := d.form.Update(msg)
	if ff, ok := f.(*huh.Form); ok {
		d.form = ff
	}
	if d.form.State == huh.StateCompleted {
		return nil, true, d.value
	}
	return cmd, false, false
}

var dialogBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tui.Accent).Padding(0, 1)

// dialogChrome is the width the dialog box's border and padding take.
const dialogChrome = 4

// view renders the dialog to fit width (wrapping long body lines).
func (d *Dialog) view(width int) string {
	var b strings.Builder
	if d.Body != "" {
		b.WriteString(strings.TrimRight(d.Body, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimRight(d.form.View(), "\n"))
	return dialogBox.Width(width - 2).Render(b.String())
}
