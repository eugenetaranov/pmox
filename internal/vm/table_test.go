package vm

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var sampleRows = []Row{
	{Name: "web1", VMID: 104, Node: "pve1", Status: "running", IP: "192.168.1.43",
		CPUs: 4, CPUUsed: 0.04, MemBytes: 21 * gib / 10, MaxMemBytes: 8 * gib, DiskBytes: 40 * gib, UptimeSec: 3*86400 + 4*3600},
	{Name: "db1", VMID: 105, Node: "pve1", Status: "stopped",
		CPUs: 2, MaxMemBytes: 4 * gib, DiskBytes: 20 * gib},
}

func render(rows []Row, o TableOpts) []string {
	var buf bytes.Buffer
	RenderTable(&buf, rows, o)
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

func TestRenderTable_Piped(t *testing.T) {
	want := []string{
		"NAME  STATE    CPU      MEM          DISK  UP     IP",
		"web1  running  4% of 4  2.1/8.0 GiB  40G   3d 4h  192.168.1.43",
		"db1   stopped  - of 2   -/4.0 GiB    20G   -      -",
	}
	got := render(sampleRows, TableOpts{})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenderTable_Terminal(t *testing.T) {
	rows := append([]Row{}, sampleRows...)
	rows = append(rows, Row{Name: "odd", Status: "paused"})
	got := render(rows, TableOpts{Terminal: true})
	for i, want := range []string{"● running  4% of 4", "○ stopped  – of 2   –/4.0 GiB    20G   –      –", "◐ paused   –"} {
		if !strings.Contains(got[i+1], want) {
			t.Errorf("row %d = %q, want it to contain %q", i, got[i+1], want)
		}
	}
	if strings.Contains(strings.Join(got, ""), "\x1b") {
		t.Errorf("no color asked for, got escape codes: %q", got)
	}
}

func TestRenderTable_ColorKeepsColumnsAligned(t *testing.T) {
	got := render(sampleRows, TableOpts{Terminal: true, Color: true})
	if !strings.Contains(got[1], "\x1b[32m● running\x1b[0m") || !strings.Contains(got[2], "\x1b[2m○ stopped\x1b[0m") {
		t.Fatalf("state cells not colored: %q", got)
	}
	strip := regexp.MustCompile(`\x1b\[[0-9]*m`)
	col := func(line, cell string) int {
		line = strip.ReplaceAllString(line, "")
		return utf8.RuneCountInString(line[:strings.Index(line, cell)])
	}
	if a, b := col(got[1], "4% of 4"), col(got[2], "– of 2"); a != b {
		t.Errorf("CPU cells start at %d and %d:\n%s\n%s", a, b, got[1], got[2])
	}
}

func TestRenderTable_WideAndNode(t *testing.T) {
	if h := render(sampleRows, TableOpts{Wide: true})[0]; !strings.HasPrefix(h, "NAME  VMID  NODE  STATE") {
		t.Errorf("--wide header = %q", h)
	}
	if h := render(sampleRows, TableOpts{ShowNode: true})[0]; !strings.HasPrefix(h, "NAME  NODE  STATE") {
		t.Errorf("multi-node header = %q", h)
	}
}

func TestRenderTable_NarrowDropsColumns(t *testing.T) {
	full := 0
	for _, l := range render(sampleRows, TableOpts{Terminal: true}) {
		full = max(full, utf8.RuneCountInString(l))
	}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{0, "NAME STATE CPU MEM DISK UP IP"},
		{full, "NAME STATE CPU MEM DISK UP IP"},
		{full - 1, "NAME STATE CPU MEM UP IP"},
		{full - 7, "NAME STATE CPU MEM IP"},
		{40, "NAME STATE CPU IP"},
		{10, "NAME STATE IP"},
	} {
		lines := render(sampleRows, TableOpts{Terminal: true, Width: tc.width})
		if got := strings.Join(strings.Fields(lines[0]), " "); got != tc.want {
			t.Errorf("width %d: header %q, want %q", tc.width, got, tc.want)
		}
		for _, l := range lines {
			if n := utf8.RuneCountInString(l); tc.width >= 40 && n > tc.width {
				t.Errorf("width %d: line is %d wide: %q", tc.width, n, l)
			}
		}
	}
}

func TestRenderTable_NameCap(t *testing.T) {
	long := strings.Repeat("x", 60)
	got := render([]Row{{Name: long, VMID: 100, Node: "pve", Status: "stopped"}}, TableOpts{})
	if strings.Contains(got[1], strings.Repeat("x", 41)) {
		t.Errorf("name not truncated to 40: %q", got[1])
	}
}

func TestRenderTable_OverlayTag(t *testing.T) {
	got := strings.Join(render([]Row{
		{Name: "a", Status: "running", IP: "100.104.54.92", Overlay: "tailscale", LANIP: "192.168.0.171", OverlayIP: "100.104.54.92", OverlayNet: "tailscale"},
		{Name: "b", Status: "running", IP: "100.90.1.2", Overlay: "netbird", OverlayIP: "100.90.1.2", OverlayNet: "netbird"},
		{Name: "c", Status: "running", IP: "192.168.0.25", LANIP: "192.168.0.25"},
		{Name: "d", Status: "running", IP: "192.168.0.30", LANIP: "192.168.0.30", OverlayIP: "100.64.0.9", OverlayNet: "tailscale"},
		{Name: "e", Status: "running", IP: "10.0.0.7"},
	}, TableOpts{}), "\n")
	for _, want := range []string{"  192.168.0.171 / 100.104.54.92 ts\n", "  100.90.1.2 nb\n", "  192.168.0.25\n", "  192.168.0.30 / 100.64.0.9 ts\n", "  10.0.0.7\n"} {
		if !strings.Contains(got+"\n", want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestSortRows(t *testing.T) {
	rows := []Row{{Name: "alpha", Status: "stopped"}, {Name: "zeta", Status: "running"}, {Name: "beta", Status: "running"}}
	SortRows(rows)
	if got := rows[0].Name + " " + rows[1].Name + " " + rows[2].Name; got != "beta zeta alpha" {
		t.Errorf("order = %s", got)
	}
}
