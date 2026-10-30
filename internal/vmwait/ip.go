package vmwait

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// skipPrefixes are interface name prefixes that PickIPv4 filters out
// on the first pass. They correspond to loopback, container runtimes,
// bridge-veth pairs, CNI, libvirt, tun/tap, and overlay networks
// (overlayPrefixes) — none of which are the VM's "real" network
// interface from an SSH-reachability standpoint.
var skipPrefixes = append([]string{"lo", "docker", "br-", "veth", "cni", "virbr", "tun"}, overlayPrefixes...)

// overlayPrefixes name overlay network interfaces: Tailscale
// (tailscale0) and NetBird (wt0).
var overlayPrefixes = []string{"tailscale", "wt"}

// WaitForIP polls the qemu-guest-agent's network-interfaces endpoint
// until it reports an interface with a usable IPv4, the context is
// cancelled, or the timeout elapses.
//
// The timeout error distinguishes between "agent never answered" (no
// successful AgentNetwork call) and "agent answered but no usable
// IPv4" (typically DHCP/netplan trouble inside the guest), so the
// caller can tell a missing guest-agent install apart from a guest
// networking failure. Both wrap pveclient.ErrTimeout.
func WaitForIP(ctx context.Context, c *pveclient.Client, node string, vmid int, timeout time.Duration, opts ...Option) (string, error) {
	cfg := newConfig(opts)
	deadline := time.Now().Add(timeout)
	var agentAnswered bool
	for {
		ifaces, err := c.AgentNetwork(ctx, node, vmid)
		if err != nil {
			// A fatal error (bad token, TLS failure, VM gone) will never
			// resolve by waiting — surface it immediately instead of
			// polling for the full timeout and then blaming the guest
			// agent. A transient error (network blip) or the expected
			// "agent not running yet" (ErrAPIError) during boot keeps
			// polling.
			if pveclient.IsFatalPollError(err) {
				return "", err
			}
		} else {
			agentAnswered = true
			if ip := PickIPv4(ifaces); ip != "" {
				return ip, nil
			}
		}
		if time.Now().After(deadline) {
			if agentAnswered {
				return "", fmt.Errorf("%w: VM %d has no usable IPv4 address after %s; guest agent is running but DHCP/network configuration in the guest did not come up (check /etc/netplan and systemd-networkd on the VM)", pveclient.ErrTimeout, vmid, timeout)
			}
			return "", fmt.Errorf("%w: qemu-guest-agent not responding on VM %d; install qemu-guest-agent in your template and re-run launch", pveclient.ErrTimeout, vmid)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(cfg.pollInterval):
		}
	}
}

// WaitForOverlayIP polls the guest agent until the VM reports an IPv4 on
// an overlay interface (Tailscale, NetBird), and returns it. It returns ""
// with no error when none appears within timeout: the VM may simply not
// be on an overlay. Errors are fatal API errors and cancellation only.
func WaitForOverlayIP(ctx context.Context, c *pveclient.Client, node string, vmid int, timeout time.Duration, opts ...Option) (string, error) {
	cfg := newConfig(opts)
	deadline := time.Now().Add(timeout)
	for {
		ifaces, err := c.AgentNetwork(ctx, node, vmid)
		if err != nil {
			if pveclient.IsFatalPollError(err) {
				return "", err
			}
		} else if ip := firstUsableIPv4(ifaces, func(name string) bool { return hasPrefix(name, overlayPrefixes) }); ip != "" {
			return ip, nil
		}
		if time.Now().After(deadline) {
			return "", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(cfg.pollInterval):
		}
	}
}

// PickIPv4 implements the D-T3 heuristic shared between launch and
// list: skip virtual / container / overlay / loopback interfaces,
// exclude loopback and link-local addresses, and fall back to any
// non-loopback non-link-local IPv4 if nothing survived the prefix
// filter. Returns the empty string when no IPv4 is usable so the caller
// can keep polling.
func PickIPv4(ifaces []pveclient.AgentIface) string {
	if ip := firstUsableIPv4(ifaces, func(name string) bool { return !hasPrefix(name, skipPrefixes) }); ip != "" {
		return ip
	}
	return firstUsableIPv4(ifaces, func(string) bool { return true })
}

// PickGuestIPv4 is PickIPv4, except that with preferOverlay it returns
// the address on the VM's overlay interface (Tailscale, NetBird) first,
// when there is one.
func PickGuestIPv4(ifaces []pveclient.AgentIface, preferOverlay bool) string {
	if preferOverlay {
		if ip := firstUsableIPv4(ifaces, func(name string) bool { return hasPrefix(name, overlayPrefixes) }); ip != "" {
			return ip
		}
	}
	return PickIPv4(ifaces)
}

// AllIPv4 returns every usable (not loopback, not link-local) IPv4 on
// every interface, in the guest agent's order.
func AllIPv4(ifaces []pveclient.AgentIface) []string {
	var out []string
	for _, iface := range ifaces {
		out = append(out, usableIPv4s(iface)...)
	}
	return out
}

// OverlayKind names the overlay network ip is on, from the interface
// that has it: "tailscale" (tailscale*), "netbird" (wt*), or "" for any
// other interface.
func OverlayKind(ifaces []pveclient.AgentIface, ip string) string {
	for _, iface := range ifaces {
		for _, a := range usableIPv4s(iface) {
			if a != ip {
				continue
			}
			switch {
			case strings.HasPrefix(iface.Name, "tailscale"):
				return "tailscale"
			case strings.HasPrefix(iface.Name, "wt"):
				return "netbird"
			}
			return ""
		}
	}
	return ""
}

// firstUsableIPv4 scans the interfaces keep accepts, in order, and
// returns the first IPv4 that isn't loopback or link-local.
func firstUsableIPv4(ifaces []pveclient.AgentIface, keep func(name string) bool) string {
	for _, iface := range ifaces {
		if !keep(iface.Name) {
			continue
		}
		if ips := usableIPv4s(iface); len(ips) > 0 {
			return ips[0]
		}
	}
	return ""
}

// usableIPv4s are iface's IPv4 addresses that aren't loopback or
// link-local.
func usableIPv4s(iface pveclient.AgentIface) []string {
	var out []string
	for _, addr := range iface.IPAddresses {
		if addr.IPAddressType != "ipv4" {
			continue
		}
		parsed := net.ParseIP(addr.IPAddress)
		if parsed == nil || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, parsed.String())
	}
	return out
}

func hasPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
