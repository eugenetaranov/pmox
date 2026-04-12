package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const listIPConcurrency = 8

type listFlags struct {
	all bool
}

func newListCmd() *cobra.Command {
	f := &listFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VMs on the resolved Proxmox cluster",
		Long: `List VMs on the resolved Proxmox cluster. By default only VMs tagged
'pmox' are shown — pass --all to include every VM.

For running VMs, pmox queries the qemu-guest-agent for an IPv4 address
in parallel (capped at 8 concurrent calls). If the agent is not up,
the IP column is left blank.

Use --output json for machine-readable output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListCmd(cmd, f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "show every VM on the cluster, not just pmox-tagged ones")
	return cmd
}

func runListCmd(cmd *cobra.Command, f *listFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := buildDeleteClient(ctx, cmd)
	if err != nil {
		return err
	}
	return executeList(ctx, cmd, client, f)
}

func executeList(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, f *listFlags) error {
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return fmt.Errorf("list cluster resources: %w", err)
	}
	rows := make([]vm.Row, 0, len(resources))
	for _, r := range resources {
		if !f.all && !vm.HasPMOXTag(r.Tags) {
			continue
		}
		rows = append(rows, vm.Row{
			Name:   r.Name,
			VMID:   r.VMID,
			Node:   r.Node,
			Status: r.Status,
		})
	}
	fetchIPs(ctx, client, rows)

	if outputMode == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	vm.RenderTable(cmd.OutOrStdout(), rows)
	return nil
}

func fetchIPs(ctx context.Context, client *pveclient.Client, rows []vm.Row) {
	sem := make(chan struct{}, listIPConcurrency)
	var wg sync.WaitGroup
	for i := range rows {
		if rows[i].Status != "running" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ifaces, err := client.AgentNetwork(ctx, rows[i].Node, rows[i].VMID)
			if err != nil {
				return
			}
			rows[i].IP = launch.PickIPv4(ifaces)
		}(i)
	}
	wg.Wait()
}
