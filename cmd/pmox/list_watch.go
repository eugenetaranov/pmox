package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// listWatchTimeout bounds one refresh, so a hung API call shows as a
// failed refresh instead of a frozen view.
const listWatchTimeout = 15 * time.Second

// listWatch is the 'pmox list --watch' view: the list, re-read every
// interval. It is read-only.
type listWatch struct {
	ctx    context.Context
	client *pveclient.Client
	flags  *listFlags
	ips    map[int]listIP
	// snap is the last good reading; err is the last refresh's failure,
	// cleared by the next success.
	snap  *listSnapshot
	err   error
	width int
}

type (
	listTickMsg   struct{}
	listLoadedMsg struct {
		snap *listSnapshot
		err  error
	}
)

func newListWatch(ctx context.Context, client *pveclient.Client, f *listFlags, width int) *listWatch {
	return &listWatch{ctx: ctx, client: client, flags: f, ips: map[int]listIP{}, width: width}
}

func runListWatch(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, f *listFlags) error {
	_, width := listTerminal(cmd.OutOrStdout())
	_, err := tea.NewProgram(newListWatch(ctx, client, f, width),
		tea.WithOutput(cmd.OutOrStdout()), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}

func (m *listWatch) Init() tea.Cmd { return m.load }

// load is one refresh. Only one runs at a time (the next tick is
// scheduled when it lands), so it may update the IP cache.
func (m *listWatch) load() tea.Msg {
	ctx, cancel := context.WithTimeout(m.ctx, listWatchTimeout)
	defer cancel()
	snap, err := loadList(ctx, m.client, m.flags, true, m.ips)
	return listLoadedMsg{snap, err}
}

func (m *listWatch) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case listLoadedMsg:
		if m.err = msg.err; msg.err == nil {
			m.snap = msg.snap
		}
		return m, tea.Tick(m.flags.interval, func(time.Time) tea.Msg { return listTickMsg{} })
	case listTickMsg:
		return m, m.load
	}
	return m, nil
}

func (m *listWatch) View() string {
	var b bytes.Buffer
	if m.snap == nil && m.err == nil {
		return "Loading VMs…\n"
	}
	if m.snap != nil {
		renderList(&b, m.snap, m.flags, true, m.width)
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(tui.Warnf(fmt.Sprintf("refresh failed: %s", strings.TrimSpace(m.err.Error()))) + "\n")
	}
	b.WriteString(tui.Muted(fmt.Sprintf("every %s · q to quit", m.flags.interval)) + "\n")
	return b.String()
}
