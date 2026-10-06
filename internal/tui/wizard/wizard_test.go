package wizard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// fakeStage records Enter calls and hands every non-shell message to
// onMsg (when set).
type fakeStage struct {
	id      string
	entered int
	ctx     context.Context
	onMsg   func(tea.Msg) tea.Cmd
}

func (s *fakeStage) ID() string       { return s.id }
func (s *fakeStage) Title() string    { return strings.ToUpper(s.id[:1]) + s.id[1:] }
func (s *fakeStage) Subtitle() string { return "sub " + s.id }
func (s *fakeStage) Enter(ctx context.Context) tea.Cmd {
	s.entered++
	s.ctx = ctx
	return nil
}
func (s *fakeStage) Update(msg tea.Msg) (Stage, tea.Cmd) {
	if s.onMsg != nil {
		return s, s.onMsg(msg)
	}
	return s, nil
}
func (s *fakeStage) View() string { return "page " + s.id }

// run executes cmd and feeds the resulting messages back into m until
// nothing is left. Commands that don't return promptly (spinner ticks,
// cursor blinks) are dropped.
func run(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(50 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case tea.QuitMsg:
		default:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func newTestModel(opts Options, stages ...*fakeStage) *Model {
	ss := make([]Stage, len(stages))
	for i, s := range stages {
		ss[i] = s
	}
	m := New(context.Background(), ss, opts)
	run(m, m.Init())
	return m
}

func TestNextAndGoTo(t *testing.T) {
	a, b, c := &fakeStage{id: "a"}, &fakeStage{id: "b"}, &fakeStage{id: "c"}
	m := newTestModel(Options{}, a, b, c)
	if m.Active() != a || a.entered != 1 {
		t.Fatalf("start: active=%s entered=%d", m.Active().ID(), a.entered)
	}
	run(m, Next())
	if m.Active() != b {
		t.Fatalf("after Next: active=%s, want b", m.Active().ID())
	}
	run(m, GoTo("a"))
	if m.Active() != a || a.entered != 2 {
		t.Fatalf("after GoTo(a): active=%s entered=%d", m.Active().ID(), a.entered)
	}
}

func TestStartOption(t *testing.T) {
	a, b := &fakeStage{id: "a"}, &fakeStage{id: "b"}
	m := newTestModel(Options{Start: "b"}, a, b)
	if m.Active() != b || a.entered != 0 {
		t.Fatalf("active=%s a.entered=%d, want b and a never entered", m.Active().ID(), a.entered)
	}
}

func TestHubCapturesNextAndEsc(t *testing.T) {
	a, b, hub := &fakeStage{id: "a"}, &fakeStage{id: "b"}, &fakeStage{id: "review"}
	m := newTestModel(Options{Hub: "review"}, a, b, hub)

	// Before the hub is reached, Esc walks back a stage.
	run(m, Next())
	run(m, func() tea.Msg { return key(tea.KeyEsc) })
	if m.Active() != a {
		t.Fatalf("esc before hub: active=%s, want a", m.Active().ID())
	}

	run(m, GoTo("review"))
	run(m, GoTo("a"))
	run(m, Next())
	if m.Active() != hub {
		t.Fatalf("Next after hub reached: active=%s, want review", m.Active().ID())
	}
	run(m, GoTo("b"))
	run(m, func() tea.Msg { return key(tea.KeyEsc) })
	if m.Active() != hub {
		t.Fatalf("Esc after hub reached: active=%s, want review", m.Active().ID())
	}
}

func TestEscOnFirstStageShowsHint(t *testing.T) {
	m := newTestModel(Options{}, &fakeStage{id: "a"}, &fakeStage{id: "b"})
	run(m, func() tea.Msg { return key(tea.KeyEsc) })
	if m.Active().ID() != "a" {
		t.Fatalf("active=%s, want a", m.Active().ID())
	}
	if !strings.Contains(m.View(), "ctrl+c to quit") {
		t.Errorf("view missing quit hint:\n%s", m.View())
	}
}

func TestStatusLines(t *testing.T) {
	a, b := &fakeStage{id: "a"}, &fakeStage{id: "b"}
	m := newTestModel(Options{}, a, b)

	run(m, Busy("probing pve"))
	if v := m.View(); !strings.Contains(v, "probing pve") {
		t.Errorf("busy label missing:\n%s", v)
	}
	run(m, Fail("nothing responding"))
	v := m.View()
	if strings.Contains(v, "probing pve") || !strings.Contains(v, "nothing responding") {
		t.Errorf("want error replacing spinner:\n%s", v)
	}
	run(m, Notice([]Line{{Warn: true, Text: "heads up"}}))
	if !strings.Contains(m.View(), "heads up") {
		t.Errorf("notice missing:\n%s", m.View())
	}
	run(m, Next())
	v = m.View()
	if strings.Contains(v, "nothing responding") || strings.Contains(v, "heads up") {
		t.Errorf("status not cleared on stage change:\n%s", v)
	}
	if !strings.Contains(v, "page b") || !strings.Contains(v, "sub b") {
		t.Errorf("view missing page/subtitle:\n%s", v)
	}
}

func TestDialogRoutesKeysAndEscPicksSafe(t *testing.T) {
	var got []bool
	stageSaw := 0
	a := &fakeStage{id: "a", onMsg: func(msg tea.Msg) tea.Cmd {
		if _, ok := msg.(tea.KeyMsg); ok {
			stageSaw++
		}
		return nil
	}}
	m := newTestModel(Options{}, a)
	ask := func(def, safe bool) {
		run(m, Ask(&Dialog{Title: "Trust?", Body: "fp SHA256:abc", Default: def, Safe: safe,
			OnResult: func(yes bool) tea.Cmd { got = append(got, yes); return nil }}))
	}

	ask(true, false)
	if v := m.View(); !strings.Contains(v, "Trust?") || !strings.Contains(v, "SHA256:abc") || strings.Contains(v, "page a") {
		t.Errorf("dialog view wrong:\n%s", v)
	}
	run(m, func() tea.Msg { return key(tea.KeyEsc) })
	if len(got) != 1 || got[0] != false {
		t.Fatalf("esc: got %v, want [false] (the safe answer)", got)
	}

	ask(true, false)
	run(m, func() tea.Msg { return key(tea.KeyEnter) })
	if len(got) != 2 || got[1] != true {
		t.Fatalf("enter: got %v, want default true", got)
	}
	if stageSaw != 0 {
		t.Errorf("stage received %d keys while a dialog was open", stageSaw)
	}
	run(m, func() tea.Msg { return key(tea.KeyEnter) })
	if stageSaw != 1 {
		t.Errorf("stage should get keys once the dialog closed (saw %d)", stageSaw)
	}
}

func TestCtrlCAbortsAndCancelsContext(t *testing.T) {
	a := &fakeStage{id: "a"}
	m := newTestModel(Options{}, a)
	run(m, func() tea.Msg { return key(tea.KeyCtrlC) })
	if !m.Done() || !errors.Is(m.Err(), tui.ErrAborted) {
		t.Fatalf("done=%v err=%v, want done + ErrAborted", m.Done(), m.Err())
	}
	if a.ctx.Err() == nil {
		t.Error("root context not cancelled on ctrl+c")
	}
	if m.View() != "" {
		t.Errorf("aborted view should be empty, got %q", m.View())
	}
}

func TestFinishLeavesSummary(t *testing.T) {
	m := newTestModel(Options{}, &fakeStage{id: "a"})
	run(m, Finish(Result{Value: 42, Summary: []Line{{Text: "configured server x"}, {Warn: true, Text: "warning: y"}}}))
	if !m.Done() || m.Err() != nil || m.Result().Value != 42 {
		t.Fatalf("done=%v err=%v value=%v", m.Done(), m.Err(), m.Result().Value)
	}
	v := m.View()
	if !strings.Contains(v, "configured server x") || !strings.Contains(v, "warning: y") || strings.Contains(v, "esc back") {
		t.Errorf("final view should be just the summary:\n%s", v)
	}
}

func TestAbortWithError(t *testing.T) {
	m := newTestModel(Options{}, &fakeStage{id: "a"})
	boom := errors.New("boom")
	run(m, Abort(boom))
	if !errors.Is(m.Err(), boom) {
		t.Fatalf("err=%v, want boom", m.Err())
	}
}

type hiddenPinned struct{ fakeStage }

func (hiddenPinned) Hidden() bool { return true }
func (hiddenPinned) Pinned() bool { return true }

func TestHiddenStageAndPinnedEsc(t *testing.T) {
	a := &fakeStage{id: "a"}
	save := &hiddenPinned{fakeStage{id: "a"}} // Title "A": highlights a's tab
	m := New(context.Background(), []Stage{a, &fakeStage{id: "b"}, save}, Options{})
	run(m, m.Init())
	run(m, func() tea.Msg { return gotoMsg{} }) // no-op (unknown id)
	m.active = 2
	v := m.View()
	if strings.Count(v, "A") != 1 {
		t.Errorf("hidden stage should not add a tab:\n%s", v)
	}
	run(m, func() tea.Msg { return key(tea.KeyEsc) })
	if m.active != 2 || !strings.Contains(m.View(), "please wait") {
		t.Errorf("esc must not leave a pinned stage (active=%d):\n%s", m.active, m.View())
	}
}

func TestFrameTitleTabsAndWrapping(t *testing.T) {
	a, b, c := &fakeStage{id: "a"}, &fakeStage{id: "b"}, &fakeStage{id: "c"}
	m := New(context.Background(), []Stage{a, b, c}, Options{Title: "pmox init"})
	run(m, m.Init())
	run(m, func() tea.Msg { return tea.WindowSizeMsg{Width: 60, Height: 30} })
	run(m, Next())

	v := m.View()
	if !strings.Contains(v, "╭─ pmox init") || !strings.Contains(v, "╰") {
		t.Errorf("missing titled frame:\n%s", v)
	}
	if !strings.Contains(v, "✓ A") || !strings.Contains(v, "● B") || strings.Contains(v, "✓ C") {
		t.Errorf("tabs should mark A done, B active, C pending:\n%s", v)
	}

	long := "nothing responding at 10.0.0.9:8006 — check the address and that Proxmox is running (dial tcp: connection refused)"
	run(m, Fail(long))
	v = m.View()
	if strings.Contains(v, "…") || !strings.Contains(v, "connection refused") {
		t.Errorf("long error must wrap, not truncate:\n%s", v)
	}
	for _, l := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
		if w := lipgloss.Width(l); w > 60 {
			t.Fatalf("line wider than the terminal (%d): %q", w, l)
		}
	}
}

func TestAltScreenFinalViewIsEmpty(t *testing.T) {
	m := New(context.Background(), []Stage{&fakeStage{id: "a"}}, Options{AltScreen: true})
	run(m, m.Init())
	run(m, Finish(Result{Summary: []Line{{Text: "configured server x"}}}))
	if m.View() != "" {
		t.Errorf("alt-screen final view should be empty (the caller prints the summary), got %q", m.View())
	}
}

type tallStage struct {
	fakeStage
	lines int
}

func (s *tallStage) View() string { return strings.Repeat("row\n", s.lines) }

func TestAltScreenFrameFitsTerminalHeight(t *testing.T) {
	for _, n := range []int{2, 60} { // shorter and taller than the terminal
		st := &tallStage{fakeStage: fakeStage{id: "a"}, lines: n}
		m := New(context.Background(), []Stage{st, &fakeStage{id: "b"}}, Options{Title: "pmox init", AltScreen: true})
		run(m, m.Init())
		run(m, func() tea.Msg { return tea.WindowSizeMsg{Width: 70, Height: 24} })
		run(m, Fail("a long error that should wrap onto more than one line inside the frame because it is wide"))
		lines := strings.Split(m.View(), "\n")
		if len(lines) != 24 {
			t.Errorf("page of %d rows: frame is %d lines, want exactly the terminal height 24", n, len(lines))
		}
		if !strings.Contains(lines[0], "╭─ pmox init") || !strings.Contains(lines[1], "A") {
			t.Errorf("page of %d rows: top border/tabs not on top:\n%s", n, m.View())
		}
		if !strings.Contains(lines[len(lines)-2], "ctrl+c quit") || !strings.Contains(lines[len(lines)-1], "╰") {
			t.Errorf("page of %d rows: footer not at the bottom:\n%s", n, m.View())
		}
	}
}
