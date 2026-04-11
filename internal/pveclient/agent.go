package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// AgentIface is one network interface as reported by the qemu-guest-agent.
type AgentIface struct {
	Name         string        `json:"name"`
	HardwareAddr string        `json:"hardware-address"`
	IPAddresses  []AgentIPAddr `json:"ip-addresses"`
}

// AgentIPAddr is one IP address on an AgentIface.
type AgentIPAddr struct {
	IPAddressType string `json:"ip-address-type"` // "ipv4" | "ipv6"
	IPAddress     string `json:"ip-address"`
	Prefix        int    `json:"prefix"`
}

// AgentNetwork issues GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces
// and returns the guest-agent's view of the VM's network interfaces.
//
// This is a single-shot call with no built-in retry. If the guest
// agent isn't running yet, PVE returns an error (typically 500) which
// surfaces as ErrAPIError — callers that want to wait for the agent
// must wrap this in their own retry loop. The retry policy is
// deliberately left to callers because it's context-specific (how
// long to wait depends on what the caller is trying to do).
func (c *Client) AgentNetwork(ctx context.Context, node string, vmid int) ([]AgentIface, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	body, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	// The PVE API wraps everything in `data`, and the guest-agent
	// response itself nests the list under `result`.
	var payload struct {
		Data struct {
			Result []AgentIface `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse agent network response: %w", err)
	}
	return payload.Data.Result, nil
}
