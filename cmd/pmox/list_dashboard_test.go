package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pvetest"
)

const dashboardVMs = `{"data":[
  {"vmid":105,"name":"db1","node":"p0","status":"stopped","tags":"pmox","maxcpu":2,"maxmem":4294967296,"maxdisk":21474836480},
  {"vmid":104,"name":"web1","node":"p0","status":"running","tags":"pmox","cpu":0.04,"maxcpu":4,"mem":2254857830,"maxmem":8589934592,"maxdisk":42949672960,"uptime":273600},
  {"vmid":200,"name":"legacy","node":"p0","status":"running","tags":""}
]}`

// dashFake is the state the dashboard fake serves, changeable mid-test.
type dashFake struct {
	agent     atomic.Int32 // guest-agent calls seen
	nodesDown atomic.Bool  // GET /nodes answers 500
	nodeCalls atomic.Int32
	db1Runs   atomic.Bool
}

// newDashboardFake serves one node, two pools and the VMs above.
func newDashboardFake(t *testing.T, d *dashFake) *pvetest.Server {
	t.Helper()
	s := pvetest.New(t)
	s.Handle("GET", "/cluster/resources", func(w http.ResponseWriter, _ *http.Request, _ string) {
		body := dashboardVMs
		if d.db1Runs.Load() {
			body = strings.Replace(body, `"name":"db1","node":"p0","status":"stopped"`, `"name":"db1","node":"p0","status":"running"`, 1)
		}
		fmt.Fprint(w, body)
	})
	s.Handle("GET", "/storage", pvetest.JSON(`{"data":[
	  {"storage":"local","type":"dir","active":1,"avail":42949672960,"total":72584527872},
	  {"storage":"vm-data","type":"lvmthin","active":1,"avail":1667527016448,"total":1965917863936}]}`))
	s.HandleExact("GET", "/nodes", func(w http.ResponseWriter, _ *http.Request, _ string) {
		d.nodeCalls.Add(1)
		if d.nodesDown.Load() {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"data":[
	  {"node":"p0","status":"online","cpu":0.02,"maxcpu":16,"mem":29742489600,"maxmem":67108864000,"uptime":871200}]}`)
	})
	s.Handle("GET", "/agent/network-get-interfaces", func(w http.ResponseWriter, r *http.Request, _ string) {
		d.agent.Add(1)
		fmt.Fprint(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"192.168.1.43"}]}]}}`)
	})
	return s
}

// asTerminal makes list treat its output as a terminal of the given width.
func asTerminal(t *testing.T, width int) {
	t.Helper()
	orig := listTerminal
	listTerminal = func(io.Writer) (bool, int) { return true, width }
	t.Cleanup(func() { listTerminal = orig })
}

func TestList_TerminalFrame(t *testing.T) {
	f := newDashboardFake(t, &dashFake{})
	asTerminal(t, 120)
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{context: "home"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if want := "home · p0 up 10d 2h · CPU 2% of 16 · mem 27.7/62.5 GiB · vm-data 1553 GiB free"; lines[0] != want {
		t.Errorf("host line = %q, want %q", lines[0], want)
	}
	if want := "1 running, 1 stopped · 1 more VM not managed by pmox (--all)"; lines[len(lines)-1] != want {
		t.Errorf("count line = %q, want %q", lines[len(lines)-1], want)
	}
	body := out.String()
	if w, d := strings.Index(body, "web1"), strings.Index(body, "db1"); w < 0 || d < w {
		t.Errorf("running web1 should come before stopped db1:\n%s", body)
	}
	if !strings.Contains(body, "4% of 4  2.1/8.0 GiB  40G   3d 4h  192.168.1.43") {
		t.Errorf("web1 row:\n%s", body)
	}
}

func TestList_AllHasNoPointer(t *testing.T) {
	f := newDashboardFake(t, &dashFake{})
	asTerminal(t, 120)
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{all: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\n2 running, 1 stopped\n") {
		t.Errorf("count line under --all:\n%s", out.String())
	}
}

func TestList_PipedIsTheTableAlone(t *testing.T) {
	d := &dashFake{}
	f := newDashboardFake(t, d)
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{context: "home"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Errorf("want a header and two rows:\n%s", out.String())
	}
	if d.nodeCalls.Load() != 0 {
		t.Error("piped list should not read the nodes")
	}
	if strings.ContainsAny(out.String(), "●○\x1b") {
		t.Errorf("piped output has glyphs or escapes:\n%q", out.String())
	}
}

func TestList_NodeFailureKeepsTheTable(t *testing.T) {
	d := &dashFake{}
	d.nodesDown.Store(true)
	f := newDashboardFake(t, d)
	asTerminal(t, 120)
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{context: "home"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "NAME") || !strings.Contains(out.String(), "1 running, 1 stopped") {
		t.Errorf("want the table and count line without a host line:\n%s", out.String())
	}
}

func TestList_WatchFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		terminal bool
		json     bool
		want     string
	}{
		{"piped", []string{"--watch"}, false, false, "needs a terminal"},
		{"json", []string{"-w"}, true, true, "-o json"},
		{"short interval", []string{"--watch", "--interval", "100ms"}, true, false, "at least 1s"},
		{"interval alone", []string{"--interval", "2s"}, true, false, "only applies with --watch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.terminal {
				asTerminal(t, 100)
			}
			if tc.json {
				outputMode = "json"
				t.Cleanup(func() { outputMode = "text" })
			}
			cmd := newListCmd()
			f := &listFlags{}
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			f.watch, _ = cmd.Flags().GetBool("watch")
			f.interval, _ = cmd.Flags().GetDuration("interval")
			err := checkListWatch(cmd, f)
			if !errors.Is(err, exitcode.ErrUserInput) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want a usage error containing %q", err, tc.want)
			}
		})
	}
	asTerminal(t, 100)
	cmd := newListCmd()
	if err := checkListWatch(cmd, &listFlags{watch: true, interval: time.Second}); err != nil {
		t.Errorf("--watch on a terminal: %v", err)
	}
}

func TestListWatch_RefreshAndQuit(t *testing.T) {
	d := &dashFake{}
	f := newDashboardFake(t, d)
	cmd, _, _ := newTestListCmd()
	m := newListWatch(cmd.Context(), f.Client(), &listFlags{context: "home", interval: 5 * time.Second}, 120)
	if !strings.Contains(m.View(), "Loading") {
		t.Errorf("first view: %q", m.View())
	}

	// Three refreshes: web1's address is read once and reused.
	for i := 0; i < 3; i++ {
		_, next := m.Update(m.load())
		if next == nil {
			t.Fatal("a refresh should schedule the next tick")
		}
		if _, load := m.Update(listTickMsg{}); load == nil {
			t.Fatal("a tick should start a refresh")
		}
	}
	if n := d.agent.Load(); n != 1 {
		t.Errorf("agent calls over three refreshes = %d, want 1", n)
	}
	view := m.View()
	for _, want := range []string{"home · p0 up", "● running", "192.168.1.43", "every 5s · q to quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}

	// A failed refresh keeps the table and shows the error; the next
	// success clears it.
	m.Update(listLoadedMsg{err: errors.New("connection refused")})
	if v := m.View(); !strings.Contains(v, "web1") || !strings.Contains(v, "refresh failed: connection refused") {
		t.Errorf("after a failed refresh:\n%s", v)
	}
	m.Update(m.load())
	if strings.Contains(m.View(), "refresh failed") {
		t.Errorf("error should clear after a good refresh:\n%s", m.View())
	}

	m.Update(tea.WindowSizeMsg{Width: 40})
	if strings.Contains(m.View(), "DISK") {
		t.Errorf("a 40-column terminal should drop DISK:\n%s", m.View())
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		_, quit := m.Update(key)
		if quit == nil {
			t.Fatalf("%s should quit", key)
		}
		if _, ok := quit().(tea.QuitMsg); !ok {
			t.Errorf("%s: got %T, want tea.QuitMsg", key, quit())
		}
	}
}

func TestListWatch_StateChangeRereadsTheAddress(t *testing.T) {
	d := &dashFake{}
	f := newDashboardFake(t, d)
	cmd, _, _ := newTestListCmd()
	m := newListWatch(cmd.Context(), f.Client(), &listFlags{interval: time.Second}, 120)
	m.Update(m.load())
	d.db1Runs.Store(true)
	m.Update(m.load())
	if n := d.agent.Load(); n != 2 {
		t.Errorf("agent calls = %d, want 2 (web1 once, db1 once it runs)", n)
	}
	if v := m.View(); strings.Index(v, "db1") > strings.Index(v, "web1") {
		t.Errorf("db1 now runs and should sort before web1:\n%s", v)
	}
}
