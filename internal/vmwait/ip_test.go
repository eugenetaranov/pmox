package vmwait

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestPickIPv4(t *testing.T) {
	tests := []struct {
		name   string
		ifaces []pveclient.AgentIface
		want   string
	}{
		{
			name: "single eth0 with one IPv4",
			ifaces: []pveclient.AgentIface{
				{Name: "eth0", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "192.168.1.10"},
				}},
			},
			want: "192.168.1.10",
		},
		{
			name: "eth0 + docker0 skips docker",
			ifaces: []pveclient.AgentIface{
				{Name: "docker0", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "172.17.0.1"},
				}},
				{Name: "eth0", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "10.0.0.5"},
				}},
			},
			want: "10.0.0.5",
		},
		{
			name: "eth0 IPv6 only falls through",
			ifaces: []pveclient.AgentIface{
				{Name: "eth0", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv6", IPAddress: "fe80::1"},
				}},
			},
			want: "",
		},
		{
			name: "link-local only falls through",
			ifaces: []pveclient.AgentIface{
				{Name: "eth0", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "169.254.1.2"},
				}},
			},
			want: "",
		},
		{
			name: "fallback path: skipped prefixes + lone ens3",
			ifaces: []pveclient.AgentIface{
				{Name: "lo", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "127.0.0.1"},
				}},
				{Name: "ens3", IPAddresses: []pveclient.AgentIPAddr{
					{IPAddressType: "ipv4", IPAddress: "10.1.2.3"},
				}},
			},
			want: "10.1.2.3",
		},
		{
			name:   "empty returns empty",
			ifaces: nil,
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PickIPv4(tc.ifaces)
			if got != tc.want {
				t.Errorf("PickIPv4 = %q, want %q", got, tc.want)
			}
		})
	}
}

// agentNetworkServer spins up a tiny test server that only answers
// the AgentNetwork endpoint. The response is controlled by the passed
// handler.
func agentNetworkServer(t *testing.T, handler func(hit int) (status int, body string)) (*pveclient.Client, func()) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/agent/network-get-interfaces") {
			http.NotFound(w, r)
			return
		}
		n := int(atomic.AddInt32(&hits, 1))
		code, body := handler(n)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	c := pveclient.New(srv.URL, "tok@pam!x", "secret", false)
	c.HTTPClient = srv.Client()
	return c, srv.Close
}

// fastPoll keeps the WaitForIP loops from sleeping the production 1s
// between polls.
var fastPoll = WithPollInterval(10 * time.Millisecond)

func TestWaitForIP_HappyPath(t *testing.T) {
	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		if hit < 3 {
			return 200, `{"data":{"result":[]}}`
		}
		return 200, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"10.5.5.5"}]}]}}`
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ip, err := WaitForIP(ctx, c, "pve", 100, 10*time.Second, fastPoll)
	if err != nil {
		t.Fatalf("WaitForIP err: %v", err)
	}
	if ip != "10.5.5.5" {
		t.Errorf("ip = %q, want 10.5.5.5", ip)
	}
}

func TestWaitForIP_Timeout(t *testing.T) {
	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		return 500, `{"data":null,"errors":{"agent":"QEMU guest agent is not running"}}`
	})
	defer stop()

	ip, err := WaitForIP(context.Background(), c, "pve", 100, 200*time.Millisecond, fastPoll)
	if err == nil {
		t.Fatalf("WaitForIP ip=%q err=nil, want timeout error", ip)
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent not responding on VM") {
		t.Errorf("err = %v, want qemu-guest-agent not responding message", err)
	}
	if !errors.Is(err, pveclient.ErrTimeout) {
		t.Errorf("err = %v, want wrapped pveclient.ErrTimeout", err)
	}
}

// A fatal error (bad token → 401) can't resolve by waiting, so
// WaitForIP must surface it immediately instead of polling for the full
// timeout and then misreporting it as "guest agent not responding".
func TestWaitForIP_FatalErrorFailsFast(t *testing.T) {
	var hits int32
	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		atomic.AddInt32(&hits, 1)
		return 401, `{"data":null}`
	})
	defer stop()

	start := time.Now()
	_, err := WaitForIP(context.Background(), c, "pve", 100, 30*time.Second, fastPoll)
	if !errors.Is(err, pveclient.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("fatal error should fail fast, took %v", elapsed)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("expected exactly 1 poll before abort, got %d", n)
	}
}

func TestWaitForIP_TimeoutNoIP(t *testing.T) {
	// Agent answers successfully, but the interface list never carries
	// a usable IPv4 (classic DHCP/netplan failure in the guest). The
	// timeout error must blame the guest network, not the agent.
	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		return 200, `{"data":{"result":[{"name":"lo","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"127.0.0.1"}]},{"name":"ens18"}]}}`
	})
	defer stop()

	ip, err := WaitForIP(context.Background(), c, "pve", 111, 200*time.Millisecond, fastPoll)
	if err == nil {
		t.Fatalf("WaitForIP ip=%q err=nil, want timeout error", ip)
	}
	if strings.Contains(err.Error(), "qemu-guest-agent not responding") {
		t.Errorf("err = %v, expected guest-network message, got agent-not-responding", err)
	}
	if !strings.Contains(err.Error(), "no usable IPv4") {
		t.Errorf("err = %v, want 'no usable IPv4' message", err)
	}
}

func TestWaitForIP_ContextCancel(t *testing.T) {
	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		return 500, `{}`
	})
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := WaitForIP(ctx, c, "pve", 100, 10*time.Second, fastPoll)
	if err == nil {
		t.Fatal("WaitForIP err=nil, want ctx.Err()")
	}
}

func TestPickGuestIPv4Overlay(t *testing.T) {
	iface := func(name, ip string) pveclient.AgentIface {
		return pveclient.AgentIface{Name: name, IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: ip}}}
	}
	lan := iface("ens18", "10.83.17.20")
	ts := iface("tailscale0", "100.101.7.3")
	nb := iface("wt0", "100.88.4.9")
	tests := []struct {
		name    string
		ifaces  []pveclient.AgentIface
		overlay bool
		want    string
	}{
		{"lan mode skips tailscale listed first", []pveclient.AgentIface{ts, lan}, false, "10.83.17.20"},
		{"lan mode skips netbird listed first", []pveclient.AgentIface{nb, lan}, false, "10.83.17.20"},
		{"lan mode falls back to a lone overlay", []pveclient.AgentIface{ts}, false, "100.101.7.3"},
		{"overlay mode prefers tailscale", []pveclient.AgentIface{lan, ts}, true, "100.101.7.3"},
		{"overlay mode prefers netbird", []pveclient.AgentIface{lan, nb}, true, "100.88.4.9"},
		{"overlay mode without overlay uses lan", []pveclient.AgentIface{iface("docker0", "172.17.0.1"), lan}, true, "10.83.17.20"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PickGuestIPv4(tc.ifaces, tc.overlay); got != tc.want {
				t.Errorf("PickGuestIPv4 = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllIPv4(t *testing.T) {
	ifaces := []pveclient.AgentIface{
		{Name: "lo", IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: "127.0.0.1"}}},
		{Name: "ens18", IPAddresses: []pveclient.AgentIPAddr{
			{IPAddressType: "ipv4", IPAddress: "10.83.17.20"},
			{IPAddressType: "ipv4", IPAddress: "169.254.3.4"},
			{IPAddressType: "ipv6", IPAddress: "fd00::1"},
		}},
		{Name: "tailscale0", IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: "100.101.7.3"}}},
	}
	got := AllIPv4(ifaces)
	if strings.Join(got, ",") != "10.83.17.20,100.101.7.3" {
		t.Errorf("AllIPv4 = %v", got)
	}
}

func TestWaitForOverlayIP(t *testing.T) {
	lanOnly := `{"data":{"result":[{"name":"ens18","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"192.168.0.171"}]}]}}`
	joined := `{"data":{"result":[{"name":"ens18","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"192.168.0.171"}]},` +
		`{"name":"tailscale0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"100.104.54.92"}]}]}}`

	c, stop := agentNetworkServer(t, func(hit int) (int, string) {
		if hit < 3 {
			return 200, lanOnly
		}
		return 200, joined
	})
	defer stop()
	ip, err := WaitForOverlayIP(context.Background(), c, "pve", 104, 10*time.Second, fastPoll)
	if err != nil || ip != "100.104.54.92" {
		t.Fatalf("WaitForOverlayIP = %q, %v; want 100.104.54.92", ip, err)
	}

	c2, stop2 := agentNetworkServer(t, func(int) (int, string) { return 200, lanOnly })
	defer stop2()
	ip, err = WaitForOverlayIP(context.Background(), c2, "pve", 104, 100*time.Millisecond, fastPoll)
	if err != nil || ip != "" {
		t.Fatalf("never joined: WaitForOverlayIP = %q, %v; want \"\", nil", ip, err)
	}
}

func TestOverlayKind(t *testing.T) {
	ifaces := []pveclient.AgentIface{
		{Name: "ens18", IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: "192.168.0.171"}}},
		{Name: "tailscale0", IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: "100.104.54.92"}}},
		{Name: "wt0", IPAddresses: []pveclient.AgentIPAddr{{IPAddressType: "ipv4", IPAddress: "100.88.4.9"}}},
	}
	for ip, want := range map[string]string{"192.168.0.171": "", "100.104.54.92": "tailscale", "100.88.4.9": "netbird", "": ""} {
		if got := OverlayKind(ifaces, ip); got != want {
			t.Errorf("OverlayKind(%q) = %q, want %q", ip, got, want)
		}
	}
}
