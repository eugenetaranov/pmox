package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

const listIPConcurrency = 8

type listFlags struct {
	all bool
}

func newListCmd() *cobra.Command {
	f := &listFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VMs",
		Long: `List the cluster's pmox VMs (the ones tagged 'pmox') with their node,
status and IP; --all lists every VM. A running VM's IP comes from its
guest agent and is blank while the agent isn't up. An address on an
overlay network is marked (tailscale) or (netbird). The default VM, if
one is set, is marked with *.`,
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
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	return executeList(ctx, cmd, client, f)
}

func executeList(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, f *listFlags) error {
	sp := startSpin("Loading VMs…")
	defer sp.Stop()
	resources, err := client.QEMUResources(ctx)
	if err != nil {
		return fmt.Errorf("list cluster resources: %w", err)
	}
	rows := make([]vm.Row, 0, len(resources))
	for _, r := range resources {
		if !f.all && !vm.HasPMOXTag(r.Tags) {
			continue
		}
		rows = append(rows, vm.Row{
			Name:    r.Name,
			VMID:    r.VMID,
			Node:    r.Node,
			Status:  r.Status,
			Default: sessionDefault.name != "" && r.Name == sessionDefault.name && vm.HasPMOXTag(r.Tags),
		})
	}
	fetchIPs(ctx, client, rows)
	sp.Stop()

	if outputMode == "json" {
		return printJSON(cmd.OutOrStdout(), rows)
	}
	vm.RenderTable(cmd.OutOrStdout(), rows)
	return nil
}

func fetchIPs(ctx context.Context, client *pveclient.Client, rows []vm.Row) {
	sem := make(chan struct{}, listIPConcurrency)
	var wg sync.WaitGroup
	for i := range rows {
		if pveclient.VMState(rows[i].Status) != pveclient.StateRunning {
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
			rows[i].IP = vmwait.PickGuestIPv4(ifaces, client.PreferOverlay)
			rows[i].Overlay = vmwait.OverlayKind(ifaces, rows[i].IP)
		}(i)
	}
	wg.Wait()
}
