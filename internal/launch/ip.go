package launch

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

const pollInterval = 1 * time.Second

// skipPrefixes are interface name prefixes that pickIPv4 filters out
// on the first pass. They correspond to loopback, container runtimes,
// bridge-veth pairs, CNI, libvirt, and tun/tap — none of which are
// the VM's "real" network interface from an SSH-reachability standpoint.
var skipPrefixes = []string{"lo", "docker", "br-", "veth", "cni", "virbr", "tun"}

// WaitForIP polls the qemu-guest-agent's network-interfaces endpoint
// until it reports an interface with a usable IPv4, the context is
// cancelled, or the timeout elapses.
//
// Any error from AgentNetwork is treated as "agent not up yet" and
// triggers a retry. The API does not distinguish "agent not running"
// from "interfaces empty", and the caller does not need to.
func WaitForIP(ctx context.Context, c *pveclient.Client, node string, vmid int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		ifaces, err := c.AgentNetwork(ctx, node, vmid)
		if err == nil {
			if ip := pickIPv4(ifaces); ip != "" {
				return ip, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("qemu-guest-agent not responding on VM %d; install qemu-guest-agent in your template and re-run launch", vmid)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// pickIPv4 implements the D-T3 heuristic: skip virtual / container /
// loopback interfaces, exclude loopback and link-local addresses, and
// fall back to any non-loopback non-link-local IPv4 if nothing survived
// the prefix filter. Returns the empty string when no IPv4 is usable
// so the caller can keep polling.
func pickIPv4(ifaces []pveclient.AgentIface) string {
	if ip := firstUsableIPv4(ifaces, true); ip != "" {
		return ip
	}
	return firstUsableIPv4(ifaces, false)
}

// firstUsableIPv4 scans the interface list in order and returns the
// first IPv4 that isn't loopback or link-local. When applyPrefixFilter
// is true, interfaces whose name starts with a skip-prefix are ignored.
func firstUsableIPv4(ifaces []pveclient.AgentIface, applyPrefixFilter bool) string {
	for _, iface := range ifaces {
		if applyPrefixFilter && hasSkipPrefix(iface.Name) {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType != "ipv4" {
				continue
			}
			parsed := net.ParseIP(addr.IPAddress)
			if parsed == nil {
				continue
			}
			if parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
				continue
			}
			return parsed.String()
		}
	}
	return ""
}

func hasSkipPrefix(name string) bool {
	for _, p := range skipPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
