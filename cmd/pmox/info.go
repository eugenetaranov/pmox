package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <name|vmid>",
		Short: "Show detailed information about a single VM",
		Long: `Print the configured and runtime state of a single VM: cpu, memory,
primary disk, status, uptime, tags, and guest-agent-reported network
interfaces. Use --output json for machine-readable output.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(cmd, args[0])
		},
	}
	return cmd
}

func runInfo(cmd *cobra.Command, arg string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := buildDeleteClient(ctx, cmd)
	if err != nil {
		return err
	}
	return executeInfo(ctx, cmd, client, arg)
}

func executeInfo(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string) error {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}
	status, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil {
		return fmt.Errorf("get status for vm %d: %w", ref.VMID, err)
	}
	cfg, err := client.GetConfig(ctx, ref.Node, ref.VMID)
	if err != nil {
		return fmt.Errorf("get config for vm %d: %w", ref.VMID, err)
	}
	var ifaces []pveclient.AgentIface
	if status.Status == "running" {
		ifaces, err = client.AgentNetwork(ctx, ref.Node, ref.VMID)
		if err != nil && !errors.Is(err, pveclient.ErrAPIError) {
			// Non-API errors (network/auth) are hard failures; agent-
			// not-running returns ErrAPIError which we swallow.
			return fmt.Errorf("agent network for vm %d: %w", ref.VMID, err)
		}
	}
	info := vm.BuildInfo(ref, status, cfg, ifaces)

	if outputMode == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}
	vm.RenderInfo(cmd.OutOrStdout(), info)
	return nil
}
