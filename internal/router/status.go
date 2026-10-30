package router

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Status is pmox router status's report.
type Status struct {
	VMID       int    `json:"vmid"`
	Node       string `json:"node"`
	State      string `json:"state"` // running, stopped
	OnBoot     bool   `json:"onboot"`
	Startup    string `json:"startup,omitempty"`
	Protected  bool   `json:"protected"`
	Address    string `json:"address,omitempty"` // eth0's IPv4 CIDR
	NodeRoute  string `json:"node_route,omitempty"`
	RouteFresh bool   `json:"route_matches_node"` // the router advertises the node's current /32
	// Egress is "filtered" (guest firewall on, and the datacenter's) or
	// "unrestricted".
	Egress    string          `json:"egress"`
	Tailscale *TailscaleState `json:"tailscale,omitempty"`
	// TailscaleErr says why Tailscale's state couldn't be read (stopped
	// container, node SSH failure).
	TailscaleErr string `json:"tailscale_error,omitempty"`
}

// StatusOptions locate the node for Report.
type StatusOptions struct {
	Bridge string
	APIURL string
	Lookup Lookup
}

// Report gathers the router's status: the container from the API, and
// Tailscale's state through pct exec when it's running.
func Report(ctx context.Context, api API, run Runner, o StatusOptions) (*Status, error) {
	r, err := Find(ctx, api)
	if err != nil {
		return nil, err
	}
	st := &Status{VMID: r.VMID, Node: r.Node, State: r.Status, Egress: "unrestricted"}
	cfg, err := api.LXCConfig(ctx, r.Node, r.VMID)
	if err != nil {
		return nil, fmt.Errorf("read container %d's config: %w", r.VMID, err)
	}
	st.OnBoot = cfg["onboot"] == "1"
	st.Startup = cfg["startup"]
	st.Protected = cfg["protection"] == "1"
	if strings.Contains(cfg["net0"], "firewall=1") {
		if on, err := api.ClusterFirewallEnabled(ctx); err == nil && on {
			if fw, err := api.LXCFirewallOptions(ctx, r.Node, r.VMID); err == nil && fw["enable"] == "1" {
				st.Egress = "filtered"
			}
		}
	}
	if route, err := NodeRoute(ctx, api, r.Node, o.Bridge, o.APIURL, o.Lookup); err == nil {
		st.NodeRoute = route
	}
	if !r.IsRunning() {
		st.TailscaleErr = "the container is not running"
		return st, nil
	}
	if ip, err := WaitIP(ctx, api, r.Node, r.VMID, 0); err == nil {
		st.Address = ip
	}
	ts, err := State(ctx, run, r.VMID)
	if err != nil {
		st.TailscaleErr = err.Error()
		return st, nil
	}
	st.Tailscale = &ts
	for _, a := range ts.Advertised {
		if a == st.NodeRoute {
			st.RouteFresh = true
		}
	}
	return st, nil
}

// Delete logs the router out (best-effort; logoutErr says why it
// couldn't), clears its protection, stops it and destroys it with its
// disks.
func Delete(ctx context.Context, api API, run Runner) (vmid int, node string, logoutErr, err error) {
	r, err := Find(ctx, api)
	if err != nil {
		return 0, "", nil, err
	}
	vmid, node = r.VMID, r.Node
	if r.IsRunning() {
		logoutErr = Logout(ctx, run, vmid)
	} else {
		logoutErr = errors.New("the container is not running")
	}
	if err := api.SetLXCConfig(ctx, node, vmid, url.Values{"protection": {"0"}}); err != nil {
		return vmid, node, logoutErr, fmt.Errorf("clear protection on %d: %w", vmid, err)
	}
	if r.IsRunning() {
		upid, err := api.LXCStop(ctx, node, vmid)
		if err != nil {
			return vmid, node, logoutErr, fmt.Errorf("stop %d: %w", vmid, err)
		}
		if err := api.WaitTask(ctx, node, upid, taskTimeout); err != nil {
			return vmid, node, logoutErr, fmt.Errorf("stop %d: %w", vmid, err)
		}
	}
	upid, err := api.DeleteLXC(ctx, node, vmid)
	if err != nil {
		return vmid, node, logoutErr, fmt.Errorf("destroy %d: %w", vmid, err)
	}
	if err := api.WaitTask(ctx, node, upid, taskTimeout); err != nil {
		return vmid, node, logoutErr, fmt.Errorf("destroy %d: %w", vmid, err)
	}
	return vmid, node, logoutErr, nil
}

// ChangeRoutes adds and removes advertised routes in place and returns
// the new list. Removing nodeRoute needs force: without it the API is
// unreachable away from home.
func ChangeRoutes(ctx context.Context, api API, run Runner, add, remove []string, nodeRoute string, force bool) ([]string, error) {
	for _, r := range add {
		if err := CheckRoute(r); err != nil {
			return nil, err
		}
	}
	if !force && nodeRoute != "" {
		for _, r := range remove {
			if r == nodeRoute {
				return nil, fmt.Errorf("%s is the Proxmox node's own route: without it pmox can't reach the API away from home (pass --force to remove it anyway)", r)
			}
		}
	}
	r, err := Find(ctx, api)
	if err != nil {
		return nil, err
	}
	if !r.IsRunning() {
		return nil, fmt.Errorf("the router container %d is not running (start it in Proxmox, or: pmox router status)", r.VMID)
	}
	ts, err := State(ctx, run, r.VMID)
	if err != nil {
		return nil, err
	}
	next := make([]string, 0, len(ts.Advertised)+len(add))
	drop := map[string]bool{}
	for _, d := range remove {
		drop[d] = true
	}
	for _, a := range ts.Advertised {
		if !drop[a] {
			next = append(next, a)
		}
	}
	for _, a := range add {
		if !slices.Contains(next, a) {
			next = append(next, a)
		}
	}
	for d := range drop {
		if !slices.Contains(ts.Advertised, d) {
			return nil, fmt.Errorf("the router doesn't advertise %s", d)
		}
	}
	if err := SetRoutes(ctx, run, r.VMID, next); err != nil {
		return nil, err
	}
	return next, nil
}
