package main

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/router"
	"github.com/eugenetaranov/pmox/internal/server"
)

// routerFakeAPI is an in-memory PVE for the router commands.
type routerFakeAPI struct {
	resources []pveclient.Resource
	created   map[string]string
	destroyed bool
}

func (f *routerFakeAPI) ClusterResources(context.Context, string) ([]pveclient.Resource, error) {
	return f.resources, nil
}
func (f *routerFakeAPI) NextID(context.Context) (int, error)                           { return 110, nil }
func (f *routerFakeAPI) WaitTask(context.Context, string, string, time.Duration) error { return nil }
func (f *routerFakeAPI) ListStorage(context.Context, string) ([]pveclient.Storage, error) {
	return []pveclient.Storage{{Storage: "local", Content: "vztmpl,rootdir", Enabled: 1}}, nil
}
func (f *routerFakeAPI) ListStorageContent(context.Context, string, string, string) ([]pveclient.StorageContent, error) {
	return []pveclient.StorageContent{{Volid: "local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst"}}, nil
}
func (f *routerFakeAPI) ListBridges(context.Context, string) ([]pveclient.Bridge, error) {
	return []pveclient.Bridge{{Iface: "vmbr0", Address: "192.168.0.185"}}, nil
}
func (f *routerFakeAPI) AplInfo(context.Context, string) ([]pveclient.Appliance, error) {
	return []pveclient.Appliance{{Template: "ubuntu-24.04-standard_24.04-2_amd64.tar.zst", Package: "ubuntu-24.04-standard"}}, nil
}
func (f *routerFakeAPI) DownloadAppliance(context.Context, string, string, string) (string, error) {
	return "UPID", nil
}
func (f *routerFakeAPI) CreateLXC(_ context.Context, _ string, _ int, kv map[string]string) (string, error) {
	f.created = kv
	return "UPID", nil
}
func (f *routerFakeAPI) LXCStart(context.Context, string, int) (string, error) { return "UPID", nil }
func (f *routerFakeAPI) LXCStop(context.Context, string, int) (string, error)  { return "UPID", nil }
func (f *routerFakeAPI) DeleteLXC(context.Context, string, int) (string, error) {
	f.destroyed = true
	return "UPID", nil
}
func (f *routerFakeAPI) LXCStatus(context.Context, string, int) (*pveclient.VMStatus, error) {
	return &pveclient.VMStatus{Status: "running"}, nil
}
func (f *routerFakeAPI) LXCConfig(context.Context, string, int) (map[string]string, error) {
	return map[string]string{"onboot": "1", "protection": "1", "startup": "order=1,up=10", "net0": "name=eth0"}, nil
}
func (f *routerFakeAPI) SetLXCConfig(context.Context, string, int, url.Values) error { return nil }
func (f *routerFakeAPI) LXCInterfaces(context.Context, string, int) ([]pveclient.LXCIface, error) {
	return []pveclient.LXCIface{{Name: "eth0", Inet: "192.168.0.37/24"}}, nil
}
func (f *routerFakeAPI) ClusterFirewallEnabled(context.Context) (bool, error) { return false, nil }
func (f *routerFakeAPI) SetLXCFirewallOptions(context.Context, string, int, url.Values) error {
	return nil
}
func (f *routerFakeAPI) AddLXCFirewallRule(context.Context, string, int, url.Values) error {
	return nil
}
func (f *routerFakeAPI) LXCFirewallOptions(context.Context, string, int) (map[string]string, error) {
	return map[string]string{}, nil
}

// routerFakeNode records node commands and stdin.
type routerFakeNode struct {
	commands, stdins []string
}

const routerStateOut = `{"BackendState":"Running","Self":{"TailscaleIPs":["100.81.31.119"],"AllowedIPs":["100.81.31.119/32"],"Online":true}}
---pmox---
{"AdvertiseRoutes":["192.168.0.185/32"]}`

func (n *routerFakeNode) run(_ context.Context, command string, stdin io.Reader, stdout, _ io.Writer) error {
	n.commands = append(n.commands, command)
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	n.stdins = append(n.stdins, in)
	if strings.Contains(command, "tailscale status --json") && stdout != nil {
		_, _ = io.WriteString(stdout, routerStateOut)
	}
	return nil
}

func fakeRouterEnv(t *testing.T, api *routerFakeAPI) *routerFakeNode {
	t.Helper()
	node := &routerFakeNode{}
	orig := routerEnvFn
	t.Cleanup(func() { routerEnvFn = orig })
	routerEnvFn = func(context.Context, *cobra.Command) (*routerEnv, error) {
		return &routerEnv{
			api: api, run: node.run, node: "p0", bridge: "vmbr0", close: func() {},
			resolved: &server.Resolved{URL: "https://192.168.0.185:8006/api2/json", Server: &config.Server{Node: "p0"}},
		}, nil
	}
	return node
}

func TestRouterCreateWithKeyFile(t *testing.T) {
	api := &routerFakeAPI{}
	node := fakeRouterEnv(t, api)
	keyFile := filepath.Join(t.TempDir(), "ts.key")
	const key = "tskey-auth-cmdtest"
	if err := os.WriteFile(keyFile, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TS_AUTHKEY", "")
	stdout, _, err := execRoot(t, "router", "create", "--auth-key-file", keyFile, "--no-input")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if api.created["tags"] != router.Tag {
		t.Errorf("created = %v", api.created)
	}
	for _, c := range node.commands {
		if strings.Contains(c, key) {
			t.Fatalf("the auth key reached a command line: %s", c)
		}
	}
	joined := false
	for i, c := range node.commands {
		if strings.Contains(c, "tailscale up") && node.stdins[i] == key {
			joined = true
		}
	}
	if !joined {
		t.Errorf("no tailscale up with the key on stdin: %q", node.commands)
	}
	for _, want := range []string{"advertising 192.168.0.185/32", "Joined the tailnet as pmox-router-p0", "Approve 192.168.0.185/32", "disable key expiry"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestRouterCreateWithoutKeyOrTerminal(t *testing.T) {
	api := &routerFakeAPI{}
	node := fakeRouterEnv(t, api)
	t.Setenv("TS_AUTHKEY", "")
	stdout, _, err := execRoot(t, "router", "create", "--auth-key-file=", "--no-input")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stdout, "pmox router login") {
		t.Errorf("stdout = %q, want a login hint", stdout)
	}
	for _, c := range node.commands {
		if strings.Contains(c, "tailscale up") {
			t.Errorf("joined without a key: %s", c)
		}
	}
}

func TestRouterStatusJSON(t *testing.T) {
	api := &routerFakeAPI{resources: []pveclient.Resource{{VMID: 110, Node: "p0", Type: "lxc", Tags: router.Tag, Status: "running"}}}
	fakeRouterEnv(t, api)
	stdout, _, err := execRoot(t, "router", "status", "--output", "json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st router.Status
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if st.VMID != 110 || st.Tailscale == nil || st.Tailscale.Backend != "Running" || st.NodeRoute != "192.168.0.185/32" || !st.RouteFresh {
		t.Errorf("status = %+v", st)
	}
}

func TestRouterStatusNoRouter(t *testing.T) {
	fakeRouterEnv(t, &routerFakeAPI{})
	_, _, err := execRoot(t, "router", "status")
	if exitcode.From(err) != exitcode.ExitNotFound {
		t.Errorf("err = %v (exit %d), want not found", err, exitcode.From(err))
	}
}

func TestRouterDeleteYes(t *testing.T) {
	api := &routerFakeAPI{resources: []pveclient.Resource{{VMID: 110, Node: "p0", Type: "lxc", Tags: router.Tag, Status: "running"}}}
	node := fakeRouterEnv(t, api)
	stdout, _, err := execRoot(t, "router", "delete", "--yes")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !api.destroyed || !strings.Contains(node.commands[0], "tailscale logout") {
		t.Errorf("destroyed=%v commands=%q", api.destroyed, node.commands)
	}
	if !strings.Contains(stdout, "Remove the device pmox-router-p0 in the Tailscale admin console") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRouterRouteGuards(t *testing.T) {
	api := &routerFakeAPI{resources: []pveclient.Resource{{VMID: 110, Node: "p0", Type: "lxc", Tags: router.Tag, Status: "running"}}}
	fakeRouterEnv(t, api)
	if _, _, err := execRoot(t, "router", "route", "rm", "192.168.0.185/32"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("removing the node route: err = %v", err)
	}
	if _, _, err := execRoot(t, "router", "route", "add", "--no-input"); exitcode.From(err) != exitcode.ExitUserError {
		t.Errorf("add without args or terminal: err = %v", err)
	}
	if _, _, err := execRoot(t, "router", "route", "add", "8.8.8.8/32"); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("public route: err = %v", err)
	}
}

func TestRenderRoutesAligned(t *testing.T) {
	var b strings.Builder
	renderRoutes(&b, &router.TailscaleState{
		Advertised: []string{"192.168.0.185/32", "192.168.0.71/32"},
		Approved:   []string{"192.168.0.185/32"},
	})
	want := "Routes:     192.168.0.185/32  approved\n" +
		"            192.168.0.71/32   awaiting approval in the Tailscale admin console\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
