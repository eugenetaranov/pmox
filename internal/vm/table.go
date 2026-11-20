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
	// Overlay is the network IP is on when it's an overlay: "tailscale"
	// or "netbird"; empty for a LAN address.
	Overlay string `json:"overlay,omitempty"`
}

// overlayTag is the IP cell's suffix for each overlay.
var overlayTag = map[string]string{"tailscale": " (tailscale)", "netbird": " (netbird)"}

const (
	nameMax = 40
	nodeMax = 20
	vmidCol = 6
	statCol = 8
	ipCol   = 15
)

// ipCell is r's IP cell: "-" when blank, and an overlay address followed
// by " (tailscale)" or " (netbird)".
func ipCell(r Row) string {
	ip := truncate(r.IP, ipCol)
	if ip == "" {
		ip = "-"
	}
	return ip + overlayTag[r.Overlay]
}

// RenderTable writes a five-column table (NAME VMID NODE STATUS IP) to w.
// Column widths per design D2: NAME max 40, NODE max 20, VMID 6,
// STATUS 8; IP is as wide as its widest cell, overlay tag included.
func RenderTable(w io.Writer, rows []Row) {
	nameW, nodeW, ipW := 4, 4, 2
	for _, r := range rows {
		if n := len(ipCell(r)); n > ipW {
			ipW = n
		}
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
	format := fmt.Sprintf("%%-%ds  %%-%dd  %%-%ds  %%-%ds  %%-%ds\n", nameW, vmidCol, nodeW, statCol, ipW)
	header := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds\n", nameW, vmidCol, nodeW, statCol, ipW)
	fmt.Fprintf(w, header, "NAME", "VMID", "NODE", "STATUS", "IP")
	for _, r := range rows {
		fmt.Fprintf(w, format, truncate(r.Name, nameMax), r.VMID, truncate(r.Node, nodeMax), truncate(r.Status, statCol), ipCell(r))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
