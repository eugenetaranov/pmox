package target

import (
	"context"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Field is one question of a Form: *Remote or *Local.
type Field interface {
	title() string
	focus(ctx context.Context, prev []string)
	update(msg tea.Msg) (cmd tea.Cmd, submitted bool)
	view(width int) string
	help() string
	answer() string
}

// Options tune Run (tests).
type Options struct {
	Output         io.Writer
	Input          io.Reader
	ProgramOptions []tea.ProgramOption
}

// Form asks its fields in order on one screen. Esc goes back to the
// previous field; Esc on the first one, or Ctrl-C, aborts.
type Form struct {
	ctx     context.Context
	fields  []Field
	i       int
	width   int
	done    bool
	aborted bool
}

// New builds a form over fields.
func New(ctx context.Context, fields ...Field) *Form {
	f := &Form{ctx: ctx, fields: fields}
	if len(fields) > 0 {
		fields[0].focus(ctx, nil)
	}
	return f
}

// Run asks fields in order and returns tui.ErrAborted when the user
// leaves without answering them all. Answers are read from the fields.
func Run(ctx context.Context, opts Options, fields ...Field) error {
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	popts := append([]tea.ProgramOption{tea.WithOutput(out), tea.WithInput(in), tea.WithContext(ctx)}, opts.ProgramOptions...)
	final, err := tea.NewProgram(New(ctx, fields...), popts...).Run()
	if err != nil {
		return tui.AbortErr(err)
	}
	if fm := final.(*Form); fm.aborted || !fm.done {
		return tui.ErrAborted
	}
	return nil
}

// Done reports whether every field was answered (tests).
func (f *Form) Done() bool { return f.done }

func (f *Form) Init() tea.Cmd { return nil }

func (f *Form) prev() []string {
	out := make([]string, f.i)
	for j := 0; j < f.i; j++ {
		out[j] = f.fields[j].answer()
	}
	return out
}

func (f *Form) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		f.width = msg.Width
		return f, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			f.aborted = true
			return f, tea.Quit
		case tea.KeyEsc:
			if f.i == 0 {
				f.aborted = true
				return f, tea.Quit
			}
			f.i--
			f.fields[f.i].focus(f.ctx, f.prev())
			return f, nil
		}
	}
	cmd, submitted := f.fields[f.i].update(msg)
	if !submitted {
		return f, cmd
	}
	if f.i == len(f.fields)-1 {
		f.done = true
		return f, tea.Quit
	}
	f.i++
	f.fields[f.i].focus(f.ctx, f.prev())
	return f, cmd
}

func (f *Form) View() string {
	var b strings.Builder
	answered := f.i
	if f.done {
		answered = len(f.fields)
	}
	for j := 0; j < answered; j++ {
		b.WriteString(accentStyle.Render("✓ ") + f.fields[j].title() + ": " + f.fields[j].answer() + "\n")
	}
	if f.done || f.aborted {
		return b.String()
	}
	cur := f.fields[f.i]
	b.WriteString(accentStyle.Render(cur.title()) + "\n")
	b.WriteString(cur.view(f.width) + "\n")
	b.WriteString(dimStyle.Render(cur.help()) + "\n")
	return b.String()
}
