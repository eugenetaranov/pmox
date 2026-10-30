package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func newHostInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show the Proxmox host: version, nodes, storage, VMs",
		Long: `Show the current context's Proxmox host: its version, each node's status,
uptime, CPU and memory use, its storage pools with free space, and how
many VMs run there (pmox's and in all). --context picks another server;
-o json prints it as JSON. For one VM, use 'pmox vm info <name>'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, resolved, err := buildClient(ctx, cmd)
			if err != nil {
				return err
			}
			info, err := collectHostInfo(ctx, client, resolved)
			if err != nil {
				return err
			}
			if outputMode == "json" {
				return printJSON(cmd.OutOrStdout(), info)
			}
			printHostInfo(cmd.OutOrStdout(), info)
			return nil
		},
	}
}

type hostStorage struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	FreeBytes int64  `json:"free_bytes"`
	SizeBytes int64  `json:"size_bytes"`
}

type hostNode struct {
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	UptimeSec   int64         `json:"uptime_seconds"`
	Cores       int           `json:"cores"`
	CPUUsed     float64       `json:"cpu_used"`
	MemBytes    int64         `json:"mem_bytes"`
	MaxMemBytes int64         `json:"max_mem_bytes"`
	Storage     []hostStorage `json:"storage"`
}

type hostInfo struct {
	Context     string     `json:"context"`
	URL         string     `json:"url"`
	Version     string     `json:"version"`
	Nodes       []hostNode `json:"nodes"`
	PmoxRunning int        `json:"pmox_vms_running"`
	PmoxStopped int        `json:"pmox_vms_stopped"`
	VMs         int        `json:"vms"`
	Templates   int        `json:"templates"`
}

func collectHostInfo(ctx context.Context, client *pveclient.Client, resolved *server.Resolved) (*hostInfo, error) {
	sp := startSpin("Reading the Proxmox host…")
	defer sp.Stop()
	info := &hostInfo{URL: resolved.URL}
	if resolved.Server != nil {
		info.Context = resolved.Server.Name
	}
	v, err := client.GetVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	info.Version = v
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range nodes {
		hn := hostNode{Name: n.Node, Status: n.Status, UptimeSec: n.Uptime, Cores: n.MaxCPU, CPUUsed: n.CPU, MemBytes: n.Mem, MaxMemBytes: n.MaxMem}
		if n.Status == "online" {
			if pools, err := client.ListStorage(ctx, n.Node); err == nil {
				for _, s := range pools {
					if s.Active == 1 && s.Total > 0 {
						hn.Storage = append(hn.Storage, hostStorage{Name: s.Storage, Type: s.Type, FreeBytes: s.Avail, SizeBytes: s.Total})
					}
				}
				sort.Slice(hn.Storage, func(i, j int) bool { return hn.Storage[i].Name < hn.Storage[j].Name })
			}
		}
		info.Nodes = append(info.Nodes, hn)
	}
	if res, err := client.QEMUResources(ctx); err == nil {
		for _, r := range res {
			switch {
			case r.IsTemplate():
				info.Templates++
			case vm.HasPMOXTag(r.Tags) && r.IsRunning():
				info.PmoxRunning++
				info.VMs++
			case vm.HasPMOXTag(r.Tags):
				info.PmoxStopped++
				info.VMs++
			default:
				info.VMs++
			}
		}
	}
	return info, nil
}

func gibStr(b int64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", float64(b)/(1<<30)), "0"), ".")
}

func uptimeStr(sec int64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func printHostInfo(w io.Writer, info *hostInfo) {
	ctx := info.Context
	if ctx == "" {
		ctx = "(unnamed)"
	}
	fmt.Fprintf(w, "Context:  %s (%s)\n", ctx, info.URL)
	fmt.Fprintf(w, "Proxmox:  VE %s\n", info.Version)
	for _, n := range info.Nodes {
		if n.Status != "online" {
			fmt.Fprintf(w, "Node:     %s — %s\n", n.Name, n.Status)
			continue
		}
		fmt.Fprintf(w, "Node:     %s — online, up %s · CPU %d cores, %.0f%% used · memory %s / %s GiB\n",
			n.Name, uptimeStr(n.UptimeSec), n.Cores, n.CPUUsed*100, gibStr(n.MemBytes), gibStr(n.MaxMemBytes))
		for _, s := range n.Storage {
			fmt.Fprintf(w, "  Storage: %-16s %s GiB free of %s (%s)\n", s.Name, gibStr(s.FreeBytes), gibStr(s.SizeBytes), s.Type)
		}
	}
	tmpl := "templates"
	if info.Templates == 1 {
		tmpl = "template"
	}
	fmt.Fprintf(w, "VMs:      %d pmox (%d running, %d stopped) · %d in all · %d %s\n",
		info.PmoxRunning+info.PmoxStopped, info.PmoxRunning, info.PmoxStopped, info.VMs, info.Templates, tmpl)
}
