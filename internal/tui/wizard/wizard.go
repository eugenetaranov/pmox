// Package wizard is a small, pmox-agnostic shell for multi-stage
// interactive flows rendered as ONE persistent bubbletea program: a fixed
// tab header, the active stage's page drawn in place below it, a status
// line (spinner, error, notices), and an optional modal dialog. Stages
// are plain tea-style models that talk to the shell only through the
// message helpers in this file (Next, GoTo, Busy, Fail, Ask, Finish, …).
//
// The shell owns the keys every stage shares: Esc goes back (to the hub
// stage once it has been reached — see Options.Hub), Ctrl-C cancels the
// root context and quits with tui.ErrAborted.
package wizard

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Stage is one page of the wizard.
type Stage interface {
	// ID is the stable identifier used by GoTo and Options.Hub/Start.
	ID() string
	// Title is the tab label; Subtitle the muted line under the tabs.
	Title() string
	Subtitle() string
	// Enter is called every time the stage becomes active. ctx is the
	// wizard's root context, cancelled on Ctrl-C.
	Enter(ctx context.Context) tea.Cmd
	Update(msg tea.Msg) (Stage, tea.Cmd)
	View() string
}

// Hidden is implemented by stages that run as a step of another tab
// (e.g. a "save" step that keeps the Review tab highlighted): they are
// left out of the tab bar, and their Title names the tab to highlight.
type Hidden interface{ Hidden() bool }

// Pinned is implemented by stages that must not be left with Esc while
// they are working (e.g. while writing configuration).
type Pinned interface{ Pinned() bool }

// Line is one status or summary line.
type Line struct {
	Warn bool
	Text string
}

// Result is what Run returns once a stage calls Finish.
type Result struct {
	Value   any    // stage-defined payload
	Summary []Line // printed in place of the wizard when it exits
}

// Options configures Run.
type Options struct {
	// Title labels the frame's top border (e.g. "pmox init").
	Title string
	// AltScreen runs the wizard full-screen. The terminal is restored on
	// exit, so the caller must print Result.Summary itself.
	AltScreen bool
	// Start is the ID of the first active stage (default: the first).
	Start string
	// Hub is a stage ID that, once entered, becomes the target of both
	// Next and Esc from every other stage — e.g. "review", so editing a
	// page from Review returns straight to Review.
	Hub string
	// Output defaults to os.Stderr; Input to os.Stdin.
	Output io.Writer
	Input  io.Reader
	// ProgramOptions are appended to the tea.Program options (tests use
	// this to drop the renderer or fix the size).
	ProgramOptions []tea.ProgramOption
}

// --- messages stages send via the helper commands below ---

type (
	nextMsg   struct{}
	gotoMsg   struct{ id string }
	busyMsg   struct{ label string }
	idleMsg   struct{}
	failMsg   struct{ text string }
	noticeMsg struct{ lines []Line }
	askMsg    struct{ d *Dialog }
	finishMsg struct{ r Result }
	abortMsg  struct{ err error }
)

func send(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

// Next advances to the following stage (or the hub once reached).
func Next() tea.Cmd { return send(nextMsg{}) }

// GoTo activates the stage with the given ID.
func GoTo(id string) tea.Cmd { return send(gotoMsg{id}) }

// Busy shows a spinner with label and clears any error.
func Busy(label string) tea.Cmd { return send(busyMsg{label}) }

// Idle stops the spinner.
func Idle() tea.Cmd { return send(idleMsg{}) }

// Fail stops the spinner and shows text as the page's error line.
func Fail(text string) tea.Cmd { return send(failMsg{text}) }

// Notice replaces the page's notice lines (nil clears them).
func Notice(lines []Line) tea.Cmd { return send(noticeMsg{lines}) }

// Ask opens d as a modal dialog.
func Ask(d *Dialog) tea.Cmd { return send(askMsg{d}) }

// Finish ends the wizard successfully with r.
func Finish(r Result) tea.Cmd { return send(finishMsg{r}) }

// Abort ends the wizard with err (tui.ErrAborted when nil).
func Abort(err error) tea.Cmd { return send(abortMsg{err}) }

// Model is the shell's tea.Model. Use Run; New is exposed for tests that
// drive the model directly.
type Model struct {
	ctx    context.Context
	cancel context.CancelFunc

	stages   []Stage
	active   int
	furthest int // highest stage index entered: earlier tabs show ✓
	hub      string
	hubSeen  bool

	title     string
	altScreen bool
	width     int // terminal size (0 until the first WindowSizeMsg)
	height    int

	spin    spinner.Model
	busy    string
	errText string
	notices []Line
	hint    string
	dialog  *Dialog

	done   bool
	result Result
	err    error
}

// New builds the shell model. ctx is the parent context; the model
// derives its own cancellable root from it.
func New(ctx context.Context, stages []Stage, opts Options) *Model {
	ctx, cancel := context.WithCancel(ctx)
	m := &Model{ctx: ctx, cancel: cancel, stages: stages, hub: opts.Hub, title: opts.Title, altScreen: opts.AltScreen}
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(tui.Accent)))
	for i, s := range stages {
		if s.ID() == opts.Start {
			m.active = i
		}
	}
	return m
}

// Run starts the wizard as an inline program and blocks until a stage
// finishes or the user aborts. The final summary stays on screen.
func Run(ctx context.Context, stages []Stage, opts Options) (Result, error) {
	if len(stages) == 0 {
		return Result{}, errors.New("wizard: no stages")
	}
	m := New(ctx, stages, opts)
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	popts := []tea.ProgramOption{tea.WithOutput(out), tea.WithInput(in), tea.WithContext(ctx)}
	if opts.AltScreen {
		popts = append(popts, tea.WithAltScreen())
	}
	popts = append(popts, opts.ProgramOptions...)
	final, err := tea.NewProgram(m, popts...).Run()
	if fm, ok := final.(*Model); ok {
		m = fm
	}
	m.cancel()
	if m.err != nil {
		return m.result, m.err
	}
	if err != nil {
		if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
			return m.result, tui.ErrAborted
		}
		return m.result, err
	}
	if !m.done {
		return m.result, tui.ErrAborted
	}
	return m.result, nil
}

// Err reports how the wizard ended (nil on Finish).
func (m *Model) Err() error { return m.err }

// Done reports whether the wizard has ended.
func (m *Model) Done() bool { return m.done }

// Result is the finished result (valid after Finish).
func (m *Model) Result() Result { return m.result }

// Active returns the active stage.
func (m *Model) Active() Stage { return m.stages[m.active] }

func (m *Model) Init() tea.Cmd { return m.enter(m.active) }

func (m *Model) enter(i int) tea.Cmd {
	m.active = i
	m.furthest = max(m.furthest, i)
	if m.stages[i].ID() == m.hub && m.hub != "" {
		m.hubSeen = true
	}
	m.busy, m.errText, m.notices, m.hint, m.dialog = "", "", nil, "", nil
	return m.stages[i].Enter(m.ctx)
}

func (m *Model) index(id string) int {
	for i, s := range m.stages {
		if s.ID() == id {
			return i
		}
	}
	return -1
}

func (m *Model) hubIndex() int {
	if !m.hubSeen {
		return -1
	}
	return m.index(m.hub)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Stages (and the huh forms inside them) lay out to the frame's
		// inner area, not the whole terminal.
		m.width, m.height = msg.Width, msg.Height
		inner := tea.WindowSizeMsg{Width: m.innerWidth(), Height: max(msg.Height-chromeLines, 5)}
		if m.dialog != nil {
			cmd, _, _ := m.dialog.update(tea.WindowSizeMsg{Width: inner.Width - dialogChrome, Height: inner.Height})
			return m, cmd
		}
		st, cmd := m.stages[m.active].Update(inner)
		m.stages[m.active] = st
		return m, cmd
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m.finish(Result{}, tui.ErrAborted)
		case tea.KeyEsc:
			if m.dialog != nil {
				return m, m.closeDialog(m.dialog.safe())
			}
			return m, m.back()
		}
		if m.dialog != nil {
			cmd, decided, v := m.dialog.update(msg)
			if decided {
				return m, m.closeDialog(v)
			}
			return m, cmd
		}
	case spinner.TickMsg:
		if m.busy == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case nextMsg:
		if h := m.hubIndex(); h >= 0 && h != m.active {
			return m, m.enter(h)
		}
		if m.active+1 < len(m.stages) {
			return m, m.enter(m.active + 1)
		}
		return m, nil
	case gotoMsg:
		if i := m.index(msg.id); i >= 0 {
			return m, m.enter(i)
		}
		return m, nil
	case busyMsg:
		start := m.busy == ""
		m.busy, m.errText, m.hint = msg.label, "", ""
		if start {
			return m, m.spin.Tick
		}
		return m, nil
	case idleMsg:
		m.busy = ""
		return m, nil
	case failMsg:
		m.busy, m.errText = "", msg.text
		return m, nil
	case noticeMsg:
		m.notices = msg.lines
		return m, nil
	case askMsg:
		m.busy, m.dialog = "", msg.d
		return m, m.dialog.init()
	case finishMsg:
		return m.finish(msg.r, nil)
	case abortMsg:
		err := msg.err
		if err == nil {
			err = tui.ErrAborted
		}
		return m.finish(Result{}, err)
	}

	// While a dialog is open, everything else belongs to it: the stage is
	// paused (stages only ask with no operation in flight).
	if m.dialog != nil {
		cmd, decided, v := m.dialog.update(msg)
		if decided {
			return m, m.closeDialog(v)
		}
		return m, cmd
	}
	s, cmd := m.stages[m.active].Update(msg)
	m.stages[m.active] = s
	return m, cmd
}

func (m *Model) closeDialog(v bool) tea.Cmd {
	d := m.dialog
	m.dialog = nil
	if d.OnResult == nil {
		return nil
	}
	return d.OnResult(v)
}

// back implements Esc: the hub once reached, else the previous stage,
// else a hint on the first page.
func (m *Model) back() tea.Cmd {
	if p, ok := m.stages[m.active].(Pinned); ok && p.Pinned() {
		m.hint = "please wait — ctrl+c to quit"
		return nil
	}
	if h := m.hubIndex(); h >= 0 && h != m.active {
		return m.enter(h)
	}
	if m.hubSeen {
		m.hint = "esc has nowhere to go back to — ctrl+c to quit"
		return nil
	}
	if m.active > 0 {
		return m.enter(m.active - 1)
	}
	m.hint = "this is the first step — ctrl+c to quit"
	return nil
}

func (m *Model) finish(r Result, err error) (tea.Model, tea.Cmd) {
	m.done, m.result, m.err = true, r, err
	m.cancel()
	return m, tea.Quit
}

var (
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5484D"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
	borderStyle = lipgloss.NewStyle().Foreground(tui.Accent)
	titleStyle  = lipgloss.NewStyle().Foreground(tui.Accent).Bold(true)
	activeTab   = lipgloss.NewStyle().Foreground(tui.Accent).Bold(true)
	doneTab     = lipgloss.NewStyle().Foreground(lipgloss.Color("#3DA35D"))
	idleTab     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
)

const (
	maxFrameWidth = 100
	// chromeLines is the frame's fixed rows around the page: top border,
	// tabs, subtitle, rule, blank, blank, rule, help, bottom border —
	// plus room for a status line and its spacer under the page.
	chromeLines = 11
	padX        = 2 // spaces between the side borders and content
)

// frameWidth is the frame's outer width.
func (m *Model) frameWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	return min(w, maxFrameWidth)
}

// innerWidth is the usable content width inside the frame.
func (m *Model) innerWidth() int { return max(m.frameWidth()-2-2*padX, 20) }

func (m *Model) View() string {
	if m.done {
		if m.err != nil || m.altScreen {
			return ""
		}
		return renderLines(m.result.Summary)
	}
	st := m.stages[m.active]

	var body []string
	if len(m.notices) > 0 {
		body = append(body, splitLines(renderLines(m.notices))...)
		body = append(body, "")
	}
	if m.dialog == nil {
		body = append(body, splitLines(strings.TrimRight(st.View(), "\n"))...)
	}
	switch {
	case m.busy != "":
		body = append(body, "", m.spin.View()+" "+m.busy)
	case m.errText != "":
		body = append(body, "")
		for _, l := range splitLines(m.errText) {
			body = append(body, errStyle.Render(l))
		}
	}
	if m.dialog != nil {
		body = append(body, "")
		body = append(body, splitLines(m.dialog.view(m.innerWidth()))...)
	}
	help := "esc back · ctrl+c quit"
	if m.hint != "" {
		help = m.hint
	}

	header := []string{m.renderTabs(st.Title())}
	if sub := st.Subtitle(); sub != "" {
		header = append(header, tui.Subtitle(sub))
	}
	return m.frame(header, body, []string{helpStyle.Render(help)})
}

// renderTabs draws the stage tabs: ✓ for stages already passed, ● for
// the active one (matched by title, so a hidden step highlights its tab).
func (m *Model) renderTabs(active string) string {
	sep := idleTab.Render("  ›  ")
	var parts []string
	for i, s := range m.stages {
		if h, ok := s.(Hidden); ok && h.Hidden() {
			continue
		}
		switch {
		case s.Title() == active:
			parts = append(parts, activeTab.Render("● "+s.Title()))
		case i < m.furthest || (i < m.active):
			parts = append(parts, doneTab.Render("✓ "+s.Title()))
		default:
			parts = append(parts, idleTab.Render(s.Title()))
		}
	}
	return strings.Join(parts, sep)
}

// frame draws a rounded box with the title in the top border and rules
// between header, body and footer. Long lines wrap (never truncate). In
// full-screen mode the frame is exactly the terminal's height: the body
// is padded so the footer sits at the bottom, or — if the page is taller
// than the terminal — cut short with a marker, so the header with its
// tabs and the footer with the keys always stay visible.
func (m *Model) frame(header, body, footer []string) string {
	w := m.frameWidth()
	inner := w - 2
	cw := inner - 2*padX
	pad := strings.Repeat(" ", padX)
	side := borderStyle.Render("│")
	wrap := func(lines []string) []string {
		var out []string
		for _, content := range lines {
			wrapped := splitLines(ansi.Wrap(content, cw, " "))
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for _, l := range wrapped {
				gap := cw - ansi.StringWidth(l)
				out = append(out, side+pad+l+strings.Repeat(" ", max(gap, 0))+pad+side)
			}
		}
		return out
	}
	rule := borderStyle.Render("├" + strings.Repeat("─", inner) + "┤")
	title := ""
	if m.title != "" {
		title = " " + titleStyle.Render(m.title) + " "
	}
	top := borderStyle.Render("╭─") + title + borderStyle.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0))+"╮")
	bottom := borderStyle.Render("╰" + strings.Repeat("─", inner) + "╯")

	head := append([]string{top}, wrap(header)...)
	head = append(head, rule)
	foot := append([]string{rule}, wrap(footer)...)
	foot = append(foot, bottom)
	mid := wrap(append(append([]string{""}, body...), ""))

	if m.altScreen && m.height > 0 {
		room := m.height - len(head) - len(foot)
		switch {
		case room <= 0:
			mid = nil
		case len(mid) > room:
			mid = append(mid[:room-1], wrap([]string{helpStyle.Render("… (enlarge the terminal to see more)")})...)
		default:
			for len(mid) < room {
				mid = append(mid, wrap([]string{""})...)
			}
		}
	}
	all := append(append(head, mid...), foot...)
	// No trailing newline: on the alternate screen it would scroll the
	// top border off by one line.
	return strings.Join(all, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func renderLines(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		if l.Warn {
			b.WriteString(tui.Warnf(l.Text))
		} else {
			b.WriteString(l.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}
