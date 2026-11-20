package vm

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Row is one line in the `pmox list` table and one object in its
// JSON mode.
type Row struct {
	Name   string `json:"name"`
	VMID   int    `json:"vmid"`
	Node   string `json:"node"`
	Status string `json:"status"`
	IP     string `json:"ip"`
	// Overlay is the network IP is on when it's an overlay: "tailscale"
	// or "netbird"; empty for a LAN address.
	Overlay string `json:"overlay,omitempty"`
	// Default marks the context's default VM ('pmox vm use').
	Default bool `json:"default,omitempty"`
	// Usage as PVE reports it: CPUUsed is the busy fraction (0..1) of
	// CPUs cores; a stopped VM has its allocation and zero use.
	CPUs        int     `json:"cpus"`
	CPUUsed     float64 `json:"cpu_used"`
	MemBytes    int64   `json:"mem_bytes"`
	MaxMemBytes int64   `json:"max_mem_bytes"`
	DiskBytes   int64   `json:"disk_bytes"`
	UptimeSec   int64   `json:"uptime_seconds"`
}

func (r Row) running() bool { return r.Status == "running" }

// TableOpts shapes RenderTable's output for where it is going.
type TableOpts struct {
	// Terminal adds the state glyph and the en dash; piped output keeps
	// to plain words and "-".
	Terminal bool
	// Color colors the state cell; only meaningful with Terminal.
	Color bool
	// Width is the terminal's width; columns are dropped until the table
	// fits. 0 keeps every column.
	Width int
	// Wide adds VMID and NODE.
	Wide bool
	// ShowNode adds NODE without Wide (a multi-node cluster).
	ShowNode bool
}

// overlayTag is the IP cell's suffix for each overlay.
var overlayTag = map[string]string{"tailscale": " ts", "netbird": " nb"}

const (
	nameMax = 40
	nodeMax = 20
	ipCol   = 15
	gib     = 1 << 30
)

// SortRows orders rows running first, then by name.
func SortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].running() != rows[j].running() {
			return rows[i].running()
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].VMID < rows[j].VMID
	})
}

// Uptime renders seconds as "3d 4h", or "2h 5m" under a day.
func Uptime(sec int64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// column is one table column; drop is the order it is given up in on a
// narrow terminal (1 first), 0 for a column that always stays.
type column struct {
	head string
	drop int
	cell func(Row) string
}

func columns(o TableOpts) []column {
	dash := "-"
	if o.Terminal {
		dash = "–"
	}
	cols := []column{{"NAME", 0, func(r Row) string { return truncate(r.Name, nameMax) }}}
	if o.Wide {
		cols = append(cols, column{"VMID", 5, func(r Row) string { return fmt.Sprint(r.VMID) }})
	}
	if o.Wide || o.ShowNode {
		cols = append(cols, column{"NODE", 6, func(r Row) string { return truncate(r.Node, nodeMax) }})
	}
	return append(cols,
		column{"STATE", 0, func(r Row) string {
			if !o.Terminal {
				return r.Status
			}
			return stateGlyph(r.Status) + " " + r.Status
		}},
		column{"CPU", 4, func(r Row) string {
			switch {
			case r.CPUs == 0:
				return dash
			case !r.running():
				return fmt.Sprintf("%s of %d", dash, r.CPUs)
			}
			return fmt.Sprintf("%.0f%% of %d", r.CPUUsed*100, r.CPUs)
		}},
		column{"MEM", 3, func(r Row) string {
			switch {
			case r.MaxMemBytes == 0:
				return dash
			case !r.running():
				return fmt.Sprintf("%s/%.1f GiB", dash, float64(r.MaxMemBytes)/gib)
			}
			return fmt.Sprintf("%.1f/%.1f GiB", float64(r.MemBytes)/gib, float64(r.MaxMemBytes)/gib)
		}},
		column{"DISK", 1, func(r Row) string {
			if r.DiskBytes == 0 {
				return dash
			}
			return fmt.Sprintf("%.0fG", float64(r.DiskBytes)/gib)
		}},
		column{"UP", 2, func(r Row) string {
			if !r.running() || r.UptimeSec == 0 {
				return dash
			}
			return Uptime(r.UptimeSec)
		}},
		column{"IP", 0, func(r Row) string {
			ip := truncate(r.IP, ipCol)
			if ip == "" {
				return dash
			}
			return ip + overlayTag[r.Overlay]
		}},
	)
}

func stateGlyph(status string) string {
	switch status {
	case "running":
		return "●"
	case "stopped":
		return "○"
	}
	return "◐"
}

// stateColor is the SGR code for a state cell: green running, dim
// stopped, yellow anything else.
func stateColor(status string) string {
	switch status {
	case "running":
		return "32"
	case "stopped":
		return "2"
	}
	return "33"
}

// RenderTable writes the `pmox list` table to w: NAME STATE CPU MEM DISK
// UP IP, plus VMID and NODE per o. Each column is as wide as its widest
// cell; on a terminal narrower than the table, columns are dropped in
// the order DISK, UP, MEM, CPU (then VMID, NODE).
func RenderTable(w io.Writer, rows []Row, o TableOpts) {
	cols := columns(o)
	// A leading marker column, only when some row is the default VM.
	marker := false
	for _, r := range rows {
		marker = marker || r.Default
	}
	widths := func() ([]int, int) {
		ws := make([]int, len(cols))
		total := 2 * (len(cols) - 1)
		if marker {
			total += 2
		}
		for i, c := range cols {
			ws[i] = utf8.RuneCountInString(c.head)
			for _, r := range rows {
				if n := utf8.RuneCountInString(c.cell(r)); n > ws[i] {
					ws[i] = n
				}
			}
			total += ws[i]
		}
		return ws, total
	}
	ws, total := widths()
	for drop := 1; o.Width > 0 && total > o.Width && drop <= 6; drop++ {
		for i, c := range cols {
			if c.drop == drop {
				cols = append(cols[:i:i], cols[i+1:]...)
				break
			}
		}
		ws, total = widths()
	}

	line := func(mark string, cell func(column) string, color string) {
		var b strings.Builder
		if marker {
			b.WriteString(mark)
		}
		for i, c := range cols {
			s := cell(c)
			pad := ""
			if i < len(cols)-1 {
				pad = strings.Repeat(" ", ws[i]-utf8.RuneCountInString(s)+2)
			}
			if c.head == "STATE" && color != "" {
				s = "\x1b[" + color + "m" + s + "\x1b[0m"
			}
			b.WriteString(s + pad)
		}
		fmt.Fprintln(w, b.String())
	}
	line("  ", func(c column) string { return c.head }, "")
	for _, r := range rows {
		mark, color := "  ", ""
		if r.Default {
			mark = "* "
		}
		if o.Terminal && o.Color {
			color = stateColor(r.Status)
		}
		line(mark, func(c column) string { return c.cell(r) }, color)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
