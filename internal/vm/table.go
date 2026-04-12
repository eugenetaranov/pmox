package vm

import (
	"fmt"
	"io"
)

// Row is one line in the `pmox list` table and one object in its
// JSON mode.
type Row struct {
	Name   string `json:"name"`
	VMID   int    `json:"vmid"`
	Node   string `json:"node"`
	Status string `json:"status"`
	IP     string `json:"ip"`
}

const (
	nameMax = 40
	nodeMax = 20
	vmidCol = 6
	statCol = 8
	ipCol   = 15
)

// RenderTable writes a fixed five-column table (NAME VMID NODE STATUS IP)
// to w. Column widths per design D2: NAME max 40, NODE max 20, VMID 6,
// STATUS 8, IP 15. Blank IP cells render as "-".
func RenderTable(w io.Writer, rows []Row) {
	nameW, nodeW := 4, 4
	for _, r := range rows {
		if n := len(r.Name); n > nameW {
			nameW = n
		}
		if n := len(r.Node); n > nodeW {
			nodeW = n
		}
	}
	if nameW > nameMax {
		nameW = nameMax
	}
	if nodeW > nodeMax {
		nodeW = nodeMax
	}
	format := fmt.Sprintf("%%-%ds  %%-%dd  %%-%ds  %%-%ds  %%-%ds\n", nameW, vmidCol, nodeW, statCol, ipCol)
	header := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds\n", nameW, vmidCol, nodeW, statCol, ipCol)
	fmt.Fprintf(w, header, "NAME", "VMID", "NODE", "STATUS", "IP")
	for _, r := range rows {
		ip := r.IP
		if ip == "" {
			ip = "-"
		}
		fmt.Fprintf(w, format, truncate(r.Name, nameMax), r.VMID, truncate(r.Node, nodeMax), truncate(r.Status, statCol), truncate(ip, ipCol))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
