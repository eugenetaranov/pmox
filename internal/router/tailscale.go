package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// setupScript prepares a fresh container: no listening services, updates
// applied unattended (Tailscale's repo included), and Tailscale installed
// in userspace mode (an unprivileged container can't get /dev/net/tun
// with an API token). It is safe to run again.
const setupScript = `set -e
export DEBIAN_FRONTEND=noninteractive
systemctl disable --now ssh.socket ssh.service >/dev/null 2>&1 || true
apt-get update -qq >/dev/null
apt-get purge -y -qq openssh-server postfix >/dev/null 2>&1 || true
apt-get install -y -qq curl ca-certificates unattended-upgrades >/dev/null
if ! command -v tailscale >/dev/null; then
  curl -fsSL https://tailscale.com/install.sh | sh >/var/log/pmox-tailscale-install.log 2>&1 || { tail -20 /var/log/pmox-tailscale-install.log >&2; exit 1; }
fi
printf '%s\n' 'Unattended-Upgrade::Origins-Pattern { "site=pkgs.tailscale.com"; };' > /etc/apt/apt.conf.d/52pmox-tailscale
systemctl enable --now unattended-upgrades >/dev/null 2>&1 || true
sed -i 's/^FLAGS=.*/FLAGS="--tun=userspace-networking"/' /etc/default/tailscaled
grep -q '^FLAGS="--tun=userspace-networking"' /etc/default/tailscaled
systemctl restart tailscaled
`

// upFlags are tailscale up's flags for the router: the routes, a fixed
// name, and nothing it doesn't need (DNS, others' routes, Tailscale SSH).
func upFlags(node string, routes []string) string {
	return fmt.Sprintf("--reset --advertise-routes=%s --hostname=%s --accept-dns=false --accept-routes=false --ssh=false",
		strings.Join(routes, ","), Hostname(node))
}

// Setup runs setupScript in the container.
func Setup(ctx context.Context, run Runner, vmid int, out io.Writer) error {
	if err := run(ctx, pctExec(vmid, setupScript), nil, out, out); err != nil {
		return fmt.Errorf("set up the router container: %w", err)
	}
	return nil
}

// Join logs the router in with an auth key. The key travels only on
// stdin into a 0600 file on the container's tmpfs, removed afterwards; it
// is never part of a command line.
func Join(ctx context.Context, run Runner, vmid int, node string, routes []string, key []byte, out io.Writer) error {
	script := `umask 077
cat > /run/pmox-ts.key
tailscale up ` + upFlags(node, routes) + ` --auth-key=file:/run/pmox-ts.key
rc=$?
rm -f /run/pmox-ts.key
exit $rc`
	if err := run(ctx, pctExec(vmid, script), bytes.NewReader(bytes.TrimSpace(key)), out, out); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	return nil
}

// Login logs the router in interactively: tailscale up prints a login
// URL to out and returns once it's approved.
func Login(ctx context.Context, run Runner, vmid int, node string, routes []string, out io.Writer) error {
	if err := run(ctx, pctExec(vmid, "tailscale up "+upFlags(node, routes)), nil, out, out); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	return nil
}

// Logout logs the router out of the tailnet. The device stays listed in
// the admin console (offline) until removed there.
func Logout(ctx context.Context, run Runner, vmid int) error {
	return run(ctx, pctExec(vmid, "tailscale logout"), nil, nil, nil)
}

// SetRoutes replaces the routes the router advertises, keeping it logged
// in.
func SetRoutes(ctx context.Context, run Runner, vmid int, routes []string) error {
	var errOut bytes.Buffer
	cmd := "tailscale set --advertise-routes=" + strings.Join(routes, ",")
	if err := run(ctx, pctExec(vmid, cmd), nil, nil, &errOut); err != nil {
		return fmt.Errorf("tailscale set: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return nil
}

// TailscaleState is what the router's tailscaled reports about itself.
type TailscaleState struct {
	Backend    string   `json:"backend"`    // Running, NeedsLogin, Stopped, ...
	IP         string   `json:"ip"`         // its IPv4 tailnet address
	Online     bool     `json:"online"`     // connected to the coordination server
	Tagged     bool     `json:"tagged"`     // a tagged device (no key expiry)
	Advertised []string `json:"advertised"` // routes it advertises
	Approved   []string `json:"approved"`   // advertised routes the tailnet approved
	Primary    []string `json:"primary"`    // routes it is currently serving
}

// Awaiting lists the advertised routes that aren't approved yet.
func (s TailscaleState) Awaiting() []string {
	var out []string
	for _, r := range s.Advertised {
		if !slices.Contains(s.Approved, r) {
			out = append(out, r)
		}
	}
	return out
}

// stateScript prints tailscale status --json, a separator, and the
// router's prefs (for the advertised routes).
const stateScript = `tailscale status --json 2>/dev/null; echo; echo ---pmox---; tailscale debug prefs 2>/dev/null`

// State reads the router's tailscaled state.
func State(ctx context.Context, run Runner, vmid int) (TailscaleState, error) {
	var out bytes.Buffer
	if err := run(ctx, pctExec(vmid, stateScript), nil, &out, nil); err != nil {
		return TailscaleState{}, fmt.Errorf("read the router's Tailscale state: %w", err)
	}
	return parseState(out.String())
}

// parseState parses stateScript's output. Approved routes are the
// device's AllowedIPs other than its own addresses; these are local-API
// fields, so missing ones are left empty.
func parseState(out string) (TailscaleState, error) {
	statusJSON, prefsJSON, _ := strings.Cut(out, "---pmox---")
	var st struct {
		BackendState string
		Self         struct {
			TailscaleIPs  []string
			AllowedIPs    []string
			PrimaryRoutes []string
			Online        bool
			Tags          []string
		}
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusJSON)), &st); err != nil {
		return TailscaleState{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	s := TailscaleState{
		Backend: st.BackendState,
		Online:  st.Self.Online,
		Tagged:  len(st.Self.Tags) > 0,
		Primary: st.Self.PrimaryRoutes,
	}
	own := map[string]bool{}
	for _, ip := range st.Self.TailscaleIPs {
		if s.IP == "" && !strings.Contains(ip, ":") {
			s.IP = ip
		}
		own[ip] = true
	}
	for _, a := range st.Self.AllowedIPs {
		addr, _, _ := strings.Cut(a, "/")
		if !own[addr] {
			s.Approved = append(s.Approved, a)
		}
	}
	var prefs struct{ AdvertiseRoutes []string }
	if strings.TrimSpace(prefsJSON) != "" {
		if err := json.Unmarshal([]byte(strings.TrimSpace(prefsJSON)), &prefs); err == nil {
			s.Advertised = prefs.AdvertiseRoutes
		}
	}
	return s, nil
}
