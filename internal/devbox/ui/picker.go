package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Tab is one picker tab.
type Tab struct{ ID, Title string }

const (
	reviewTab = "__review__"
	searchTab = "__search__"
)

var (
	accent   = lipgloss.NewStyle().Foreground(tui.Accent)
	bold     = lipgloss.NewStyle().Bold(true)
	muted    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8A8A"))
	green    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	tabOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1A1A1A")).Background(tui.Accent)
	accentBd = accent.Bold(true)
)

// Picker is the tabbed checklist: categories as tabs, type to search every
// tab and mise's registry, space toggles, Review last. Only space changes
// a selection.
type Picker struct {
	title   string
	tabs    []Tab
	entries []*Entry
	review  bool

	tab            int
	query          string
	results        []*Entry
	cursor, top    map[string]int
	width, height  int
	confirmed, out bool
}

// NewPicker builds a picker over entries (catalog rows, then mise tools,
// which only appear in search results). review adds the Review tab.
func NewPicker(title string, tabs []Tab, entries []*Entry, review bool) *Picker {
	p := &Picker{title: title, entries: entries, review: review, cursor: map[string]int{}, top: map[string]int{}, width: 100, height: 30}
	for _, t := range tabs {
		for _, e := range entries {
			if e.Tab == t.ID {
				p.tabs = append(p.tabs, t)
				break
			}
		}
	}
	if review {
		p.tabs = append(p.tabs, Tab{reviewTab, "Review"})
	}
	return p
}

// Selected returns the ticked ids, in entry order.
func (p *Picker) Selected() []string {
	var out []string
	for _, e := range p.entries {
		if e.On {
			out = append(out, e.ID)
		}
	}
	return out
}

// Confirmed reports whether the user confirmed (not quit).
func (p *Picker) Confirmed() bool { return p.confirmed }

func (p *Picker) Init() tea.Cmd { return nil }

func (p *Picker) view() string {
	if p.query != "" {
		return searchTab
	}
	return p.tabs[p.tab].ID
}

func (p *Picker) items(view string) []*Entry {
	switch view {
	case searchTab:
		return p.results
	case reviewTab:
		var out []*Entry
		for _, e := range p.entries {
			if e.On {
				out = append(out, e)
			}
		}
		return out
	}
	var out []*Entry
	for _, e := range p.entries {
		if e.Tab == view {
			out = append(out, e)
		}
	}
	return out
}

func (p *Picker) setQuery(q string) {
	p.query = q
	p.results = nil
	if q != "" {
		p.results = Search(p.entries, q)
	}
	p.cursor[searchTab], p.top[searchTab] = 0, 0
}

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		return p, nil
	case tea.KeyMsg:
		return p, p.key(m)
	}
	return p, nil
}

func (p *Picker) key(k tea.KeyMsg) tea.Cmd {
	view := p.view()
	items := p.items(view)
	cur := p.cursor[view]
	last := len(items) - 1
	if last < 0 {
		last = 0
	}
	clamp := func(n int) int { return max(0, min(n, last)) }
	switch k.Type {
	case tea.KeyDown:
		p.cursor[view] = clamp(cur + 1)
	case tea.KeyUp:
		p.cursor[view] = clamp(cur - 1)
	case tea.KeyHome:
		p.cursor[view] = 0
	case tea.KeyEnd:
		p.cursor[view] = last
	case tea.KeyPgDown:
		p.cursor[view] = clamp(cur + 10)
	case tea.KeyPgUp:
		p.cursor[view] = clamp(cur - 10)
	case tea.KeySpace:
		if len(items) > 0 {
			items[cur].On = !items[cur].On
			if view != reviewTab {
				p.cursor[view] = clamp(cur + 1)
			}
		}
	case tea.KeyBackspace:
		if p.query != "" {
			r := []rune(p.query)
			p.setQuery(string(r[:len(r)-1]))
		}
	case tea.KeyEsc, tea.KeyCtrlC:
		if p.query == "" || k.Type == tea.KeyCtrlC {
			return tea.Quit
		}
		p.setQuery("")
	case tea.KeyEnter:
		switch {
		case p.query != "":
			p.setQuery("")
			if !p.review {
				p.confirmed = true
				return tea.Quit
			}
			p.tab = len(p.tabs) - 1
		case p.tab == len(p.tabs)-1:
			p.confirmed = true
			return tea.Quit
		default:
			p.tab++
		}
	case tea.KeyRight, tea.KeyTab:
		if p.query == "" {
			p.tab = (p.tab + 1) % len(p.tabs)
		}
	case tea.KeyLeft, tea.KeyShiftTab:
		if p.query == "" {
			p.tab = (p.tab - 1 + len(p.tabs)) % len(p.tabs)
		}
	case tea.KeyRunes:
		s := string(k.Runes)
		if p.query == "" && len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < len(p.tabs) {
			p.tab = int(s[0] - '1')
			return nil
		}
		if strings.TrimSpace(s) != "" {
			p.setQuery(p.query + s)
		}
	}
	return nil
}

// wrap splits s into lines at most w cells wide, at spaces where it can.
func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		for runewidth.StringWidth(word) > w {
			cut := runewidth.Truncate(word, w, "")
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, cut)
			word = word[len(cut):]
		}
		switch {
		case line == "":
			line = word
		case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= w:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

func (p *Picker) tabTitle(id string) string {
	if id == MiseTab {
		return "mise"
	}
	for _, t := range p.tabs {
		if t.ID == id {
			return t.Title
		}
	}
	return ""
}

func (p *Picker) View() string {
	if p.out {
		return ""
	}
	w, h := p.width, p.height
	var b strings.Builder
	chosen := len(p.Selected())
	right := fmt.Sprintf("%d selected", chosen)
	rs := muted
	if chosen > 0 {
		rs = green
	}
	gap := max(1, w-2-runewidth.StringWidth(p.title)-runewidth.StringWidth(right)-1)
	b.WriteString(" " + accentBd.Render(p.title) + strings.Repeat(" ", gap) + rs.Render(right) + "\n\n")

	view := p.view()
	if view == searchTab {
		n := len(p.results)
		plus := ""
		if n >= MaxResults {
			plus = "+"
		}
		es := "es"
		if n == 1 {
			es = ""
		}
		b.WriteString(" " + muted.Render("Search: ") + accentBd.Render(p.query+"_") + "  " + muted.Render(fmt.Sprintf("%d%s match%s", n, plus, es)) + "\n")
	} else {
		b.WriteString(p.tabBar(w, chosen) + "\n")
	}
	b.WriteString(muted.Render(strings.Repeat("─", max(1, w-1))) + "\n")

	items := p.items(view)
	listH := max(1, h-6)
	cur := min(p.cursor[view], max(0, len(items)-1))
	p.cursor[view] = cur

	labelW := 10
	srcHead := 0
	if view == searchTab {
		for _, e := range items {
			srcHead = max(srcHead, runewidth.StringWidth(p.tabTitle(e.Tab)))
		}
		srcHead = min(14, max(4, srcHead))
		labelW = max(14, w-7-srcHead-3)
	} else {
		for _, e := range items {
			labelW = max(labelW, runewidth.StringWidth(e.Label))
		}
		labelW = min(42, labelW+2, max(14, (w-7)/2))
	}
	srcX := 7 + labelW
	srcW := max(4, w-srcX-3)
	type row struct {
		lab, src []string
		h        int
	}
	rows := make([]row, len(items))
	for i, e := range items {
		var lab []string
		if e.Tab == MiseTab {
			lab = []string{runewidth.Truncate(e.Label, labelW-2, "…")}
		} else {
			lab = wrap(e.Label, labelW-2)
		}
		rt := e.Source
		if view == searchTab {
			rt = p.tabTitle(e.Tab)
		}
		src := wrap(rt, srcW)
		rows[i] = row{lab, src, max(len(lab), len(src))}
	}
	top := p.top[view]
	if cur < top {
		top = cur
	}
	for top < cur {
		sum := 0
		for _, r := range rows[top : cur+1] {
			sum += r.h
		}
		if sum <= listH {
			break
		}
		top++
	}
	p.top[view] = top

	lines := 0
	if len(items) == 0 {
		msg := "(empty)"
		switch view {
		case searchTab:
			msg = "No matches — backspace to edit, esc to clear."
		case reviewTab:
			msg = "Nothing selected yet — go back with ← and tick items with space."
		}
		b.WriteString("\n   " + muted.Render(msg) + "\n")
		lines = 2
	}
	for i := top; i < len(items) && lines < listH; i++ {
		e, r := items[i], rows[i]
		sel := i == cur
		for n := 0; n < r.h && lines < listH; n++ {
			mark, box := "  ", "    "
			if n == 0 {
				if sel {
					mark = accentBd.Render("›") + " "
				}
				box = muted.Render("[ ]") + " "
				if e.On {
					box = green.Render("[x]") + " "
				}
			}
			lab := ""
			if n < len(r.lab) {
				lab = r.lab[n]
				if sel {
					lab = bold.Render(lab)
				}
			}
			pad := max(0, labelW-runewidth.StringWidth(r.lab[min(n, len(r.lab)-1)]))
			if n >= len(r.lab) {
				pad = labelW
			}
			src := ""
			if n < len(r.src) {
				src = muted.Render(r.src[n])
			}
			b.WriteString(" " + mark + box + lab + strings.Repeat(" ", pad) + src + "\n")
			lines++
		}
	}
	for ; lines < listH; lines++ {
		b.WriteString("\n")
	}
	keys := "type to search · ←/→ category · ↑/↓ move · space toggle · enter next · esc quit"
	switch view {
	case searchTab:
		keys = "esc clear · ↑/↓ move · space toggle · enter review"
		if !p.review {
			keys = "esc clear · ↑/↓ move · space toggle · enter confirm"
		}
	case reviewTab:
		keys = "space untick · ← back · type to search · enter confirm · esc quit"
	}
	b.WriteString(" " + muted.Render(keys))
	return b.String()
}

func (p *Picker) tabBar(w, chosen int) string {
	names := make([]string, len(p.tabs))
	for i, t := range p.tabs {
		k := chosen
		if t.ID != reviewTab {
			k = 0
			for _, e := range p.items(t.ID) {
				if e.On {
					k++
				}
			}
		}
		names[i] = " " + t.Title + " "
		if k > 0 {
			names[i] = fmt.Sprintf(" %s (%d) ", t.Title, k)
		}
	}
	first := 0
	width := func(from, to int) int {
		n := 0
		for _, s := range names[from : to+1] {
			n += runewidth.StringWidth(s) + 1
		}
		return n
	}
	for width(first, p.tab) > w-6 && first < p.tab {
		first++
	}
	var b strings.Builder
	b.WriteString(" ")
	x := 1
	if first > 0 {
		b.WriteString(muted.Render("‹") + " ")
		x += 2
	}
	for i := first; i < len(names); i++ {
		if x+runewidth.StringWidth(names[i]) >= w-2 {
			b.WriteString(muted.Render("›"))
			break
		}
		if i == p.tab {
			b.WriteString(tabOn.Render(names[i]))
		} else {
			b.WriteString(muted.Render(names[i]))
		}
		b.WriteString(" ")
		x += runewidth.StringWidth(names[i]) + 1
	}
	return b.String()
}

// Run shows the picker on the terminal; it returns the ticked ids, or
// ok=false when the user quit.
func (p *Picker) Run() (ids []string, ok bool, err error) {
	if _, err := tea.NewProgram(p, tea.WithAltScreen()).Run(); err != nil {
		return nil, false, err
	}
	p.out = true
	return p.Selected(), p.confirmed, nil
}
