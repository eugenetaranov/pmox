package vm

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Info is the aggregated detail view shown by `pmox info`.
type Info struct {
	Name        string      `json:"name"`
	VMID        int         `json:"vmid"`
	Node        string      `json:"node"`
	Status      string      `json:"status"`
	Tags        string      `json:"tags"`
	Template    string      `json:"template,omitempty"`
	CPU         int         `json:"cpu"`
	MemMB       int         `json:"mem_mb"`
	DiskSize    string      `json:"disk_size,omitempty"`
	DiskStorage string      `json:"disk_storage,omitempty"`
	Uptime      int64       `json:"uptime_sec,omitempty"`
	Interfaces  []Interface `json:"interfaces,omitempty"`
}

// Interface is one guest-agent reported network interface.
type Interface struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac,omitempty"`
	IPs  []string `json:"ips,omitempty"`
}

// BuildInfo composes an Info from the resolved Ref, a status response,
// the raw config map, and optional guest-agent interface list.
func BuildInfo(ref *Ref, status *pveclient.VMStatus, cfg map[string]string, ifaces []pveclient.AgentIface) Info {
	info := Info{
		Name:   ref.Name,
		VMID:   ref.VMID,
		Node:   ref.Node,
		Status: status.Status,
		Tags:   ref.Tags,
		Uptime: status.Uptime,
	}
	if v, ok := cfg["cores"]; ok {
		info.CPU, _ = strconv.Atoi(v)
	}
	if v, ok := cfg["memory"]; ok {
		info.MemMB, _ = strconv.Atoi(v)
	}
	if v, ok := cfg["template"]; ok && v == "1" {
		info.Template = "yes"
	}
	if v, ok := cfg["scsi0"]; ok {
		info.DiskStorage, info.DiskSize = parseDisk(v)
	}
	for _, ifc := range ifaces {
		var ips []string
		for _, a := range ifc.IPAddresses {
			ips = append(ips, a.IPAddress)
		}
		info.Interfaces = append(info.Interfaces, Interface{Name: ifc.Name, MAC: ifc.HardwareAddr, IPs: ips})
	}
	return info
}

// parseDisk splits a scsi0-style config value like
// "local-lvm:vm-104-disk-0,size=20G" into storage name and size.
func parseDisk(raw string) (storage, size string) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return "", ""
	}
	if i := strings.Index(parts[0], ":"); i > 0 {
		storage = parts[0][:i]
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "size=") {
			size = strings.TrimPrefix(p, "size=")
		}
	}
	return
}

// RenderInfo writes the human-readable info block to w.
func RenderInfo(w io.Writer, info Info) {
	fmt.Fprintf(w, "Name:     %s\n", info.Name)
	fmt.Fprintf(w, "VMID:     %d\n", info.VMID)
	fmt.Fprintf(w, "Node:     %s\n", info.Node)
	fmt.Fprintf(w, "Status:   %s", info.Status)
	if info.Uptime > 0 {
		fmt.Fprintf(w, " (up %ds)", info.Uptime)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Tags:     %s\n", info.Tags)
	if info.Template != "" {
		fmt.Fprintf(w, "Template: %s\n", info.Template)
	}
	fmt.Fprintf(w, "CPU:      %d cores\n", info.CPU)
	fmt.Fprintf(w, "Memory:   %d MB\n", info.MemMB)
	if info.DiskSize != "" || info.DiskStorage != "" {
		fmt.Fprintf(w, "Disk:     %s on %s\n", info.DiskSize, info.DiskStorage)
	}
	for _, ifc := range info.Interfaces {
		fmt.Fprintf(w, "Network:  %s %s\n", ifc.Name, strings.Join(ifc.IPs, " "))
	}
}
