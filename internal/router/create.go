package router

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Timeouts for the API tasks create waits on.
const (
	downloadTimeout = 10 * time.Minute
	taskTimeout     = 3 * time.Minute
	netTimeout      = 90 * time.Second
)

// CreateOptions are pmox router create's settings.
type CreateOptions struct {
	Node    string
	Bridge  string
	Storage string   // the server's storage; used for the root disk when it holds containers
	Routes  []string // validated routes to advertise
	// IP and Gateway give the container a static address (IP is a CIDR);
	// empty means DHCP. Firewall needs IP.
	IP       string
	Gateway  string
	Firewall bool
	// Progress reports each step; may be nil.
	Progress func(string)
}

func (o CreateOptions) step(msg string) {
	if o.Progress != nil {
		o.Progress(msg)
	}
}

// Create builds the router container: the OS template (downloaded when
// missing), an unprivileged container on the bridge, the optional guest
// firewall, start, and the in-container setup. It returns the VMID; the
// Tailscale join is separate (Join or Login).
func Create(ctx context.Context, api API, run Runner, o CreateOptions) (int, error) {
	if _, err := Find(ctx, api); err == nil {
		return 0, errors.New("a pmox router already exists (see: pmox router status)")
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	if o.Firewall {
		if o.IP == "" {
			return 0, errors.New("--firewall needs a static address: pass --ip <cidr> and --gw <gateway>")
		}
		on, err := api.ClusterFirewallEnabled(ctx)
		if err != nil {
			return 0, fmt.Errorf("read the datacenter firewall setting: %w", err)
		}
		if !on {
			return 0, errors.New("--firewall: the datacenter firewall is off, and guest firewall rules do nothing without it. " +
				"pmox won't turn it on for you: enabling it also applies the host's input rules, which can lock you out of the node. " +
				"Enable it yourself (Datacenter → Firewall → Options), then run create again, or create without --firewall")
		}
	}

	o.step("Choosing the OS template…")
	pools, err := api.ListStorage(ctx, o.Node)
	if err != nil {
		return 0, fmt.Errorf("list storage: %w", err)
	}
	tmplStore, rootStore, err := pickStorage(pools, o.Storage)
	if err != nil {
		return 0, err
	}
	ostemplate, err := ensureTemplate(ctx, api, o, tmplStore)
	if err != nil {
		return 0, err
	}

	o.step("Creating the router container…")
	vmid, err := createContainer(ctx, api, o, ostemplate, rootStore)
	if err != nil {
		return 0, err
	}
	if o.Firewall {
		o.step("Setting up the guest firewall…")
		if err := setupFirewall(ctx, api, o, vmid); err != nil {
			return vmid, err
		}
	}
	o.step("Starting the router container…")
	upid, err := api.LXCStart(ctx, o.Node, vmid)
	if err != nil {
		return vmid, fmt.Errorf("start container %d: %w", vmid, err)
	}
	if err := api.WaitTask(ctx, o.Node, upid, taskTimeout); err != nil {
		return vmid, fmt.Errorf("start container %d: %w", vmid, err)
	}
	o.step("Waiting for its network…")
	if _, err := WaitIP(ctx, api, o.Node, vmid, netTimeout); err != nil {
		return vmid, err
	}
	o.step("Installing Tailscale (userspace mode) and removing sshd…")
	if err := Setup(ctx, run, vmid, nil); err != nil {
		return vmid, err
	}
	return vmid, nil
}

// pickStorage chooses where the template and the root disk go: the
// first enabled storage with vztmpl content, and the server's storage
// when it holds containers (rootdir), else the first that does.
func pickStorage(pools []pveclient.Storage, preferred string) (tmpl, root string, err error) {
	for _, p := range pools {
		if p.Enabled == 0 && p.Active == 0 {
			continue
		}
		if tmpl == "" && p.HasContent("vztmpl") {
			tmpl = p.Storage
		}
		if p.HasContent("rootdir") && (root == "" || p.Storage == preferred) {
			root = p.Storage
		}
	}
	switch {
	case tmpl == "":
		return "", "", errors.New("no enabled storage holds container templates (content type vztmpl); add it to one, e.g. local")
	case root == "":
		return "", "", errors.New("no enabled storage holds container disks (content type rootdir)")
	}
	return tmpl, root, nil
}

// ensureTemplate returns the ostemplate volume for the router's OS,
// downloading it from the appliance index when the node doesn't have it.
func ensureTemplate(ctx context.Context, api API, o CreateOptions, storage string) (string, error) {
	apps, err := api.AplInfo(ctx, o.Node)
	if err != nil {
		return "", fmt.Errorf("read the appliance index: %w", err)
	}
	app, ok := PickTemplate(apps)
	if !ok {
		return "", errors.New("the node's appliance index has no Ubuntu LTS or Debian standard template (refresh it on the node: pveam update)")
	}
	volid := storage + ":vztmpl/" + app.Template
	have, err := api.ListStorageContent(ctx, o.Node, storage, "vztmpl")
	if err != nil {
		return "", fmt.Errorf("list templates on %s: %w", storage, err)
	}
	for _, c := range have {
		if c.Volid == volid {
			return volid, nil
		}
	}
	o.step("Downloading " + app.Template + "…")
	upid, err := api.DownloadAppliance(ctx, o.Node, storage, app.Template)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", app.Template, err)
	}
	if err := api.WaitTask(ctx, o.Node, upid, downloadTimeout); err != nil {
		return "", fmt.Errorf("download %s: %w", app.Template, err)
	}
	return volid, nil
}

// createContainer creates the router container and returns its VMID.
func createContainer(ctx context.Context, api API, o CreateOptions, ostemplate, rootStore string) (int, error) {
	ip := "ip=dhcp"
	if o.IP != "" {
		ip = "ip=" + o.IP
		if o.Gateway != "" {
			ip += ",gw=" + o.Gateway
		}
	}
	net0 := fmt.Sprintf("name=eth0,bridge=%s,hwaddr=%s,%s", o.Bridge, randomMAC(), ip)
	if o.Firewall {
		net0 += ",firewall=1"
	}
	kv := map[string]string{
		"ostemplate":   ostemplate,
		"hostname":     Tag,
		"unprivileged": "1",
		"features":     "nesting=1",
		"net0":         net0,
		"rootfs":       rootStore + ":3",
		"memory":       "256",
		"swap":         "256",
		"cores":        "1",
		"onboot":       "1",
		"startup":      "order=1,up=10",
		"protection":   "1",
		"tags":         Tag,
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		vmid, err := api.NextID(ctx)
		if err != nil {
			return 0, fmt.Errorf("pick a VMID: %w", err)
		}
		upid, err := api.CreateLXC(ctx, o.Node, vmid, kv)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "already exists") {
				continue // another create took the ID: pick again
			}
			return 0, fmt.Errorf("create the router container: %w", err)
		}
		if err := api.WaitTask(ctx, o.Node, upid, taskTimeout); err != nil {
			return vmid, fmt.Errorf("create the router container %d: %w", vmid, err)
		}
		return vmid, nil
	}
	return 0, fmt.Errorf("create the router container: %w", lastErr)
}

// randomMAC is a locally administered address in Proxmox's BC:24:11
// prefix, fixed in the container's config so DHCP keeps its address.
func randomMAC() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("BC:24:11:%02X:%02X:%02X", b[0], b[1], b[2])
}

// WaitIP polls the container's interfaces until eth0 has an IPv4, and
// returns it (as a CIDR).
func WaitIP(ctx context.Context, api API, node string, vmid int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		ifs, err := api.LXCInterfaces(ctx, node, vmid)
		if err == nil {
			for _, f := range ifs {
				if f.Name == "eth0" && f.Inet != "" {
					return f.Inet, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("container %d has no IPv4 on eth0 after %s (DHCP on the bridge?)", vmid, timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// setupFirewall turns on the guest firewall: inbound only Tailscale's
// UDP port, outbound only the node's API and SSH, Tailscale, DNS and web
// (apt, Tailscale's control and relays).
func setupFirewall(ctx context.Context, api API, o CreateOptions, vmid int) error {
	nodeIPs := make([]string, 0, len(o.Routes))
	for _, r := range o.Routes {
		if _, n, err := net.ParseCIDR(r); err == nil {
			nodeIPs = append(nodeIPs, n.String())
		}
	}
	rules := []url.Values{
		{"type": {"in"}, "action": {"ACCEPT"}, "proto": {"udp"}, "dport": {"41641"}, "comment": {"tailscale"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"tcp"}, "dest": {strings.Join(nodeIPs, ",")}, "comment": {"routed hosts"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"udp"}, "dport": {"41641"}, "comment": {"tailscale"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"udp"}, "dport": {"3478"}, "comment": {"tailscale stun"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"tcp"}, "dport": {"80,443"}, "comment": {"tailscale control, apt"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"udp"}, "dport": {"53"}, "comment": {"dns"}},
		{"type": {"out"}, "action": {"ACCEPT"}, "proto": {"tcp"}, "dport": {"53"}, "comment": {"dns"}},
	}
	for _, r := range rules {
		r.Set("enable", "1")
		if err := api.AddLXCFirewallRule(ctx, o.Node, vmid, r); err != nil {
			return fmt.Errorf("add a firewall rule: %w", err)
		}
	}
	opts := url.Values{
		"enable":     {"1"},
		"policy_in":  {"DROP"},
		"policy_out": {"DROP"},
		"ipfilter":   {"1"},
		"macfilter":  {"1"},
	}
	if err := api.SetLXCFirewallOptions(ctx, o.Node, vmid, opts); err != nil {
		return fmt.Errorf("enable the guest firewall: %w", err)
	}
	return nil
}
