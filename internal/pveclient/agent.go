package pveclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
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
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid)
	// The PVE API wraps everything in `data`, and the guest-agent
	// response itself nests the list under `result`.
	data, err := getData[struct {
		Result []AgentIface `json:"result"`
	}](ctx, c, path, nil, "agent network response")
	if err != nil {
		return nil, err
	}
	return data.Result, nil
}

var (
	// ErrVMNotRunning marks a guest-agent call against a stopped VM.
	ErrVMNotRunning = errors.New("vm is not running")
	// ErrAgentNotRunning marks a guest-agent call against a running VM
	// whose qemu-guest-agent isn't answering.
	ErrAgentNotRunning = errors.New("qemu guest agent is not running")
)

// agentErr classifies PVE's guest-agent failures (500s with a message)
// into ErrVMNotRunning / ErrAgentNotRunning; anything else is returned
// unchanged (403 still matches ErrForbidden).
func agentErr(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode < 500 {
		return err
	}
	text := strings.ToLower(apiErr.Message + " " + string(apiErr.body))
	switch {
	case strings.Contains(text, "guest agent is not running"), strings.Contains(text, "qemu guest agent"):
		return fmt.Errorf("%w: %w", ErrAgentNotRunning, err)
	case strings.Contains(text, "not running"):
		return fmt.Errorf("%w: %w", ErrVMNotRunning, err)
	}
	return err
}

// AgentFileRead reads path inside the guest via the qemu-guest-agent
// (GET .../agent/file-read). PVE caps reads at 16 MiB; truncated reports
// whether the file was longer than what was returned.
func (c *Client) AgentFileRead(ctx context.Context, node string, vmid int, path string) (content []byte, truncated bool, err error) {
	p := fmt.Sprintf("/nodes/%s/qemu/%d/agent/file-read", url.PathEscape(node), vmid)
	data, err := getData[struct {
		Content   string          `json:"content"`
		Truncated json.RawMessage `json:"truncated"`
	}](ctx, c, p, url.Values{"file": {path}}, "agent file-read response")
	if err != nil {
		return nil, false, agentErr(err)
	}
	// PVE has reported this as both a boolean and an integer.
	t := strings.TrimSpace(string(data.Truncated))
	return []byte(data.Content), t == "true" || (t != "" && t != "0" && t != "false" && t != "null"), nil
}

// AgentFileWrite writes content to path inside the guest via the
// qemu-guest-agent (POST .../agent/file-write). The guest agent opens
// the file for truncating write, so an existing file keeps its owner and
// mode; a new file is created by the agent (as root). PVE limits content
// to 60 KiB.
//
// The content is base64-encoded here and sent with encode=0: PVE's own
// encoding fails on any non-ASCII byte ("Wide character in subroutine
// entry"), e.g. a UTF-8 comment in an authorized_keys file.
func (c *Client) AgentFileWrite(ctx context.Context, node string, vmid int, path string, content []byte) error {
	p := fmt.Sprintf("/nodes/%s/qemu/%d/agent/file-write", url.PathEscape(node), vmid)
	form := url.Values{"file": {path}, "content": {base64.StdEncoding.EncodeToString(content)}, "encode": {"0"}}
	if _, err := c.requestForm(ctx, "POST", p, form); err != nil {
		return agentErr(err)
	}
	return nil
}
