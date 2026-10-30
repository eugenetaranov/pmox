package pveclient

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// LXC containers: only what `pmox router` needs. The calls mirror the
// qemu ones in vm.go under /nodes/{node}/lxc.

func lxcPath(node string, vmid int, suffix string) string {
	return fmt.Sprintf("/nodes/%s/lxc/%d%s", url.PathEscape(node), vmid, suffix)
}

// CreateLXC issues POST /nodes/{node}/lxc with kv as the container's
// settings (ostemplate, hostname, net0, rootfs, ...); vmid is added
// automatically. Returns the UPID of the create task.
func (c *Client) CreateLXC(ctx context.Context, node string, vmid int, kv map[string]string) (string, error) {
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(vmid))
	for k, v := range kv {
		form.Set(k, v)
	}
	body, err := c.requestForm(ctx, "POST", fmt.Sprintf("/nodes/%s/lxc", url.PathEscape(node)), form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// LXCStart starts a container and returns the task's UPID.
func (c *Client) LXCStart(ctx context.Context, node string, vmid int) (string, error) {
	return c.lxcTask(ctx, "POST", lxcPath(node, vmid, "/status/start"), nil)
}

// LXCStop stops a container (no guest shutdown) and returns the task's
// UPID.
func (c *Client) LXCStop(ctx context.Context, node string, vmid int) (string, error) {
	return c.lxcTask(ctx, "POST", lxcPath(node, vmid, "/status/stop"), nil)
}

// DeleteLXC destroys a stopped container with its disks (purge, and
// unreferenced disks) and returns the task's UPID.
func (c *Client) DeleteLXC(ctx context.Context, node string, vmid int) (string, error) {
	q := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}
	body, err := c.request(ctx, "DELETE", lxcPath(node, vmid, ""), q)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

func (c *Client) lxcTask(ctx context.Context, method, path string, form url.Values) (string, error) {
	body, err := c.requestForm(ctx, method, path, form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// LXCStatus returns GET /nodes/{node}/lxc/{vmid}/status/current.
func (c *Client) LXCStatus(ctx context.Context, node string, vmid int) (*VMStatus, error) {
	st, err := getData[VMStatus](ctx, c, lxcPath(node, vmid, "/status/current"), nil, "container status response")
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// LXCConfig returns a container's config as a string map, like
// GetConfig.
func (c *Client) LXCConfig(ctx context.Context, node string, vmid int) (map[string]string, error) {
	return c.getConfigMap(ctx, lxcPath(node, vmid, "/config"))
}

// SetLXCConfig updates a container's config (PUT .../config).
func (c *Client) SetLXCConfig(ctx context.Context, node string, vmid int, form url.Values) error {
	_, err := c.requestForm(ctx, "PUT", lxcPath(node, vmid, "/config"), form)
	return err
}

// LXCIface is one network interface a running container reports.
type LXCIface struct {
	Name string `json:"name"`
	Inet string `json:"inet"` // IPv4 CIDR, "" when none
}

// LXCInterfaces returns GET /nodes/{node}/lxc/{vmid}/interfaces (PVE
// 8.1+): the container's interfaces and addresses, empty while it's
// stopped.
func (c *Client) LXCInterfaces(ctx context.Context, node string, vmid int) ([]LXCIface, error) {
	return getData[[]LXCIface](ctx, c, lxcPath(node, vmid, "/interfaces"), nil, "container interfaces response")
}

// Appliance is one entry of a node's appliance (CT template) index.
type Appliance struct {
	Template string `json:"template"` // e.g. "ubuntu-24.04-standard_24.04-2_amd64.tar.zst"
	Package  string `json:"package"`  // e.g. "ubuntu-24.04-standard"
	Version  string `json:"version"`
	Section  string `json:"section"`
	OS       string `json:"os"`
}

// AplInfo returns the node's appliance index (GET /nodes/{node}/aplinfo).
func (c *Client) AplInfo(ctx context.Context, node string) ([]Appliance, error) {
	return getData[[]Appliance](ctx, c, fmt.Sprintf("/nodes/%s/aplinfo", url.PathEscape(node)), nil, "aplinfo response")
}

// DownloadAppliance downloads template from the appliance index to
// storage and returns the task's UPID.
func (c *Client) DownloadAppliance(ctx context.Context, node, storage, template string) (string, error) {
	form := url.Values{"storage": {storage}, "template": {template}}
	return c.lxcTask(ctx, "POST", fmt.Sprintf("/nodes/%s/aplinfo", url.PathEscape(node)), form)
}

// ClusterFirewallEnabled reports whether the datacenter firewall is on
// (GET /cluster/firewall/options; a missing "enable" means off). Guest
// firewall rules have no effect while it is off.
func (c *Client) ClusterFirewallEnabled(ctx context.Context) (bool, error) {
	opts, err := getData[struct {
		Enable flexInt `json:"enable"`
	}](ctx, c, "/cluster/firewall/options", nil, "firewall options response")
	if err != nil {
		return false, err
	}
	return opts.Enable != 0, nil
}

// SetLXCFirewallOptions sets a container's firewall options (enable,
// policy_out, ipfilter, ...).
func (c *Client) SetLXCFirewallOptions(ctx context.Context, node string, vmid int, form url.Values) error {
	_, err := c.requestForm(ctx, "PUT", lxcPath(node, vmid, "/firewall/options"), form)
	return err
}

// AddLXCFirewallRule adds one rule to a container's firewall (type,
// action, dest, dport, proto, enable, ...).
func (c *Client) AddLXCFirewallRule(ctx context.Context, node string, vmid int, form url.Values) error {
	_, err := c.requestForm(ctx, "POST", lxcPath(node, vmid, "/firewall/rules"), form)
	return err
}

// LXCFirewallOptions returns a container's firewall options, as a
// string map.
func (c *Client) LXCFirewallOptions(ctx context.Context, node string, vmid int) (map[string]string, error) {
	return c.getConfigMap(ctx, lxcPath(node, vmid, "/firewall/options"))
}
