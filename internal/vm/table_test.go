package vm

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTable_GoldenThreeRows(t *testing.T) {
	rows := []Row{
		{Name: "web1", VMID: 104, Node: "pve1", Status: "running", IP: "192.168.1.43"},
		{Name: "db1", VMID: 105, Node: "pve1", Status: "stopped"},
		{Name: "worker", VMID: 200, Node: "pve2", Status: "running", IP: "10.0.0.2"},
	}
	var buf bytes.Buffer
	RenderTable(&buf, rows)
	got := buf.String()

	// Header must include the fixed column labels in order.
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "VMID") ||
		!strings.Contains(got, "NODE") || !strings.Contains(got, "STATUS") ||
		!strings.Contains(got, "IP") {
		t.Errorf("header missing columns: %q", got)
	}
	if !strings.Contains(got, "web1") || !strings.Contains(got, "192.168.1.43") {
		t.Errorf("row 1 missing: %q", got)
	}
	// Stopped VM renders IP as "-".
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	var db1Line string
	for _, l := range lines {
		if strings.HasPrefix(l, "db1") {
			db1Line = l
		}
	}
	if db1Line == "" {
		t.Fatalf("no db1 line in output: %q", got)
	}
	if !strings.Contains(db1Line, " - ") && !strings.HasSuffix(strings.TrimRight(db1Line, " "), "-") {
		t.Errorf("db1 line should render blank IP as '-': %q", db1Line)
	}
	// Header + 3 data rows.
	if n := len(lines); n != 4 {
		t.Errorf("line count = %d, want 4: %q", n, got)
	}
}

func TestRenderTable_NameCap(t *testing.T) {
	long := strings.Repeat("x", 60)
	rows := []Row{{Name: long, VMID: 1, Node: "n", Status: "running", IP: "1.2.3.4"}}
	var buf bytes.Buffer
	RenderTable(&buf, rows)
	out := buf.String()
	// NAME column caps at 40 so the long name is truncated.
	if strings.Contains(out, strings.Repeat("x", 41)) {
		t.Errorf("long name not truncated to 40: %q", out)
	}
}
