// Package palette is pmox's interactive command palette: a framed,
// filterable list of commands grouped into sections, where groups open
// in place (with a breadcrumb and Esc to go back). It returns the chosen
// command path, e.g. ["vm", "list"].
package palette

import (
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Item is one row.
type Item struct {
	Key     string // path segment, e.g. "list"
	Desc    string // one-line summary
	Hint    string // shown for the selected row, e.g. "pmox vm list"
	Section string // section header the item is listed under
	Sub     bool   // opens a nested menu instead of running
}

// Menu is one level of the palette.
type Menu struct {
	Items []Item
}

// Resolver returns the menu for a group path (e.g. ["vm"]).
type Resolver func(path []string) Menu

// Options configures Run.
type Options struct {
	Title   string // frame title, e.g. "pmox"
	Context string // shown at the top right, e.g. the current context
	Output  io.Writer
	Input   io.Reader
	// ProgramOptions are appended (tests).
	ProgramOptions []tea.ProgramOption
}

// ErrAborted is returned when the user leaves without choosing.
var ErrAborted = tui.ErrAborted

// Run shows the palette starting at start (the root when empty) and
// returns the full chosen path.
func Run(start []string, resolve Resolver, opts Options) ([]string, error) {
	m := New(start, resolve, opts)
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	popts := append([]tea.ProgramOption{tea.WithOutput(out), tea.WithInput(in)}, opts.ProgramOptions...)
	final, err := tea.NewProgram(m, popts...).Run()
	if err != nil {
		return nil, err
	}
	fm := final.(*Model)
	if fm.chosen == nil {
		return nil, ErrAborted
	}
	return fm.chosen, nil
}

// Model is the palette's tea.Model (exported for tests).
type Model struct {
	resolve Resolver
	opts    Options
	base    []string // the path Run started at (can't go above it)
	path    []string
	menu    Menu
	filter  string
	cursor  int // index into visible()
	width   int
	height  int
	chosen  []string
	done    bool
}

// New builds the model.
func New(start []string, resolve Resolver, opts Options) *Model {
	m := &Model{resolve: resolve, opts: opts, base: append([]string{}, start...), path: append([]string{}, start...)}
	m.menu = resolve(m.path)
	return m
}

// Chosen is the selected path (nil if aborted or still open).
func (m *Model) Chosen() []string { return m.chosen }

// Done reports whether the palette closed.
func (m *Model) Done() bool { return m.done }

// Path is the menu currently shown.
func (m *Model) Path() []string { return m.path }

func (m *Model) Init() tea.Cmd { return nil }

// visible returns the items matching the filter. Name-prefix matches
// rank above name or description matches.
func (m *Model) visible() []Item {
	f := strings.ToLower(strings.TrimSpace(m.filter))
	if f == "" {
		return m.menu.Items
	}
	var prefix, other []Item
	for _, it := range m.menu.Items {
		key, desc := strings.ToLower(it.Key), strings.ToLower(it.Desc+" "+it.Hint)
		switch {
		case strings.HasPrefix(key, f):
			prefix = append(prefix, it)
		case strings.Contains(key, f) || strings.Contains(desc, f):
			other = append(other, it)
		}
	}
	return append(prefix, other...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	vis := m.visible()
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.quit(nil)
	case tea.KeyEsc:
		switch {
		case m.filter != "":
			m.filter, m.cursor = "", 0
		case len(m.path) > len(m.base):
			m.pop()
		default:
			return m.quit(nil)
		}
	case tea.KeyUp, tea.KeyShiftTab, tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		} else if len(vis) > 0 {
			m.cursor = len(vis) - 1
		}
	case tea.KeyDown, tea.KeyTab, tea.KeyCtrlN:
		if m.cursor < len(vis)-1 {
			m.cursor++
		} else {
			m.cursor = 0
		}
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = max(len(vis)-1, 0)
	case tea.KeyLeft:
		if m.filter == "" && len(m.path) > len(m.base) {
			m.pop()
		}
	case tea.KeyRight:
		if len(vis) > 0 && vis[m.cursor].Sub {
			m.push(vis[m.cursor].Key)
		}
	case tea.KeyEnter:
		if len(vis) == 0 {
			return m, nil
		}
		it := vis[m.cursor]
		if it.Sub {
			m.push(it.Key)
			return m, nil
		}
		return m.quit(append(append([]string{}, m.path...), it.Key))
	case tea.KeyBackspace:
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.cursor = 0
		} else if len(m.path) > len(m.base) {
			m.pop()
		}
	case tea.KeySpace:
		m.filter += " "
		m.cursor = 0
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.cursor = 0
	}
	return m, nil
}

func (m *Model) push(key string) {
	m.path = append(m.path, key)
	m.menu, m.filter, m.cursor = m.resolve(m.path), "", 0
}

func (m *Model) pop() {
	left := m.path[len(m.path)-1]
	m.path = m.path[:len(m.path)-1]
	m.menu, m.filter, m.cursor = m.resolve(m.path), "", 0
	for i, it := range m.menu.Items { // land back on the group we left
		if it.Key == left {
			m.cursor = i
		}
	}
}

func (m *Model) quit(chosen []string) (tea.Model, tea.Cmd) {
	m.chosen, m.done = chosen, true
	return m, tea.Quit
}

// --- view ---

var (
	fg           = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"}
	borderStyle  = lipgloss.NewStyle().Foreground(tui.Accent)
	titleStyle   = lipgloss.NewStyle().Foreground(tui.Accent).Bold(true)
	crumbStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9A9A9A"))
	sectionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7A7A7A")).Bold(true)
	keyStyle     = lipgloss.NewStyle().Foreground(fg)
	selKeyStyle  = lipgloss.NewStyle().Foreground(tui.Accent).Bold(true)
	descStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8A8A"))
	selDescStyle = lipgloss.NewStyle().Foreground(fg)
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C")).Italic(true)
	barStyle     = lipgloss.NewStyle().Foreground(tui.Accent)
	ctxStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#3DA35D"))
	keyHelp      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9A9A9A"))
	keyHelpDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
)

const maxWidth = 92

func (m *Model) View() string {
	if m.done {
		return ""
	}
	w := m.width
	if w <= 0 {
		w = 80
	}
	w = min(w, maxWidth)
	inner := w - 2 // inside the side borders
	cw := inner - 4

	row := func(content string) string {
		content = ansi.Truncate(content, cw, "…")
		gap := max(cw-ansi.StringWidth(content), 0)
		return borderStyle.Render("│") + "  " + content + strings.Repeat(" ", gap) + "  " + borderStyle.Render("│")
	}

	// Header: title + breadcrumb on the left, context on the right.
	title := " " + titleStyle.Render(m.opts.Title)
	for _, p := range m.path {
		title += crumbStyle.Render(" › ") + titleStyle.Render(p)
	}
	title += " "
	right := ""
	if m.opts.Context != "" {
		right = " " + ctxStyle.Render("● "+m.opts.Context) + " "
	}
	fill := max(inner-2-ansi.StringWidth(title)-ansi.StringWidth(right), 1)
	top := borderStyle.Render("╭─") + title + borderStyle.Render(strings.Repeat("─", fill)) + right + borderStyle.Render("─╮")

	// Filter line.
	filter := descStyle.Render("⌕  ") + keyStyle.Render(m.filter) + barStyle.Render("▏")
	if m.filter == "" {
		filter = descStyle.Render("⌕  ") + barStyle.Render("▏") + hintStyle.Render("type to filter")
	}

	lines, cursorLine := m.body(cw)

	// Scroll: keep the cursor's line in view within the terminal height,
	// marking hidden lines above/below.
	room := len(lines)
	if m.height > 0 {
		room = max(m.height-8, 3)
	}
	if total := len(lines); total > room {
		start := min(max(cursorLine-room/2, 0), total-room)
		lines = append([]string{}, lines[start:start+room]...)
		if start > 0 {
			lines[0] = descStyle.Render("  ↑ more")
		}
		if start+room < total {
			lines[len(lines)-1] = descStyle.Render("  ↓ more")
		}
	}

	// Footer.
	k := func(key, what string) string { return keyHelp.Render(key) + " " + keyHelpDim.Render(what) }
	esc := "quit"
	if len(m.path) > len(m.base) {
		esc = "back"
	}
	help := strings.Join([]string{k("↑↓", "move"), k("enter", "select"), k("type", "filter"), k("esc", esc)}, keyHelpDim.Render("  ·  "))

	rule := borderStyle.Render("├" + strings.Repeat("─", inner) + "┤")
	var b strings.Builder
	b.WriteString(top + "\n")
	b.WriteString(row(filter) + "\n")
	b.WriteString(rule + "\n")
	for _, l := range lines {
		b.WriteString(row(l) + "\n")
	}
	b.WriteString(rule + "\n")
	b.WriteString(row(help) + "\n")
	b.WriteString(borderStyle.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// body renders the section headers and items for width cw, returning
// the lines and the index of the cursor's line.
func (m *Model) body(cw int) ([]string, int) {
	vis := m.visible()
	if len(vis) == 0 {
		return []string{descStyle.Render(fmt.Sprintf("nothing matches %q", m.filter))}, 0
	}
	keyW := 0
	sections := map[string]bool{}
	for _, it := range vis {
		keyW = max(keyW, ansi.StringWidth(it.Key))
		sections[it.Section] = true
	}
	// While filtering, or with a single section, headers are noise.
	showHeaders := m.filter == "" && len(sections) > 1

	var lines []string
	cursorLine := 0
	section := "\x00"
	for i, it := range vis {
		if showHeaders && it.Section != section {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, sectionStyle.Render(strings.ToUpper(it.Section)))
			section = it.Section
		}
		name := it.Key + strings.Repeat(" ", keyW-ansi.StringWidth(it.Key))
		var line string
		if i == m.cursor {
			cursorLine = len(lines)
			line = barStyle.Render("▌ ") + selKeyStyle.Render(name) + "  " + selDescStyle.Render(it.Desc)
			if it.Hint != "" {
				line += "  " + hintStyle.Render(it.Hint)
			}
		} else {
			line = "  " + keyStyle.Render(name) + "  " + descStyle.Render(it.Desc)
		}
		// Groups get a right-aligned › so they read as "opens a menu".
		if it.Sub {
			arrow := descStyle.Render("›")
			if i == m.cursor {
				arrow = barStyle.Render("›")
			}
			line = ansi.Truncate(line, cw-3, "…")
			line += strings.Repeat(" ", max(cw-1-ansi.StringWidth(line), 1)) + arrow
		}
		lines = append(lines, line)
	}
	return lines, cursorLine
}
