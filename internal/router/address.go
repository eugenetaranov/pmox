package router

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

// overlayNet is the CGNAT range Tailscale and NetBird use by default; a
// node address there is the node's overlay address, not its LAN one.
var overlayNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// Lookup resolves a host name to addresses.
type Lookup func(ctx context.Context, host string) ([]string, error)

// NodeRoute is the route the router advertises: the node's own LAN IPv4
// as a /32. It comes from the bridge's address on the node, else from
// the API URL's host (resolved with lookup when it's a name). The
// address must be private (RFC 1918) and not in 100.64.0.0/10.
func NodeRoute(ctx context.Context, api API, node, bridge, apiURL string, lookup Lookup) (string, error) {
	if bridges, err := api.ListBridges(ctx, node); err == nil {
		for _, b := range bridges {
			if b.Iface == bridge && b.Address != "" {
				return routeFor(b.Address, "bridge "+bridge)
			}
		}
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("can't tell the node's address from %q; pass --route <ip>/32", apiURL)
	}
	host := u.Hostname()
	addrs := []string{host}
	if net.ParseIP(host) == nil {
		if lookup == nil {
			return "", fmt.Errorf("can't resolve %s; pass --route <ip>/32", host)
		}
		if addrs, err = lookup(ctx, host); err != nil {
			return "", fmt.Errorf("resolve %s: %w; pass --route <ip>/32", host, err)
		}
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return routeFor(a, "the API host "+host)
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address; pass --route <ip>/32", host)
}

// routeFor checks addr is a LAN IPv4 and returns it as a /32.
func routeFor(addr, from string) (string, error) {
	ip := net.ParseIP(addr).To4()
	switch {
	case ip == nil:
		return "", fmt.Errorf("%s: %q is not an IPv4 address; pass --route <ip>/32", from, addr)
	case overlayNet.Contains(ip):
		return "", fmt.Errorf("%s is %s, an overlay (Tailscale/NetBird) address, not the node's LAN one; pass --route <ip>/32", from, ip)
	case !ip.IsPrivate():
		return "", fmt.Errorf("%s is %s, not a private LAN address; pass --route <ip>/32", from, ip)
	}
	return ip.String() + "/32", nil
}

// privateNets are the ranges a route may fall in (RFC 1918).
var privateNets = []*net.IPNet{
	{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
	{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
}

// CheckRoute validates a route to advertise: an IPv4 CIDR with no host
// bits, inside a private range, and no wider than /16, so a typo can't
// pull public traffic (or a whole private /8) through the router.
func CheckRoute(route string) error {
	ip, n, err := net.ParseCIDR(route)
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("route %q: want an IPv4 CIDR such as 192.168.0.185/32", route)
	}
	if !ip.Equal(n.IP) {
		return fmt.Errorf("route %q has host bits set; did you mean %s?", route, n)
	}
	inside := false
	for _, p := range privateNets {
		pOnes, _ := p.Mask.Size()
		ones, _ := n.Mask.Size()
		if p.Contains(n.IP) && ones >= pOnes {
			inside = true
		}
	}
	if !inside {
		return fmt.Errorf("route %s is not inside a private range (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)", n)
	}
	if ones, _ := n.Mask.Size(); ones < 16 {
		return fmt.Errorf("route %s is wider than /16; advertise the addresses you need", n)
	}
	return nil
}
