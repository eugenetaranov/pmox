package router

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func baseAPI() *fakeAPI {
	return &fakeAPI{
		storage: []pveclient.Storage{
			{Storage: "local", Content: "iso,vztmpl,backup", Enabled: 1},
			{Storage: "local-lvm", Content: "images,rootdir", Enabled: 1},
			{Storage: "vm-data", Content: "images,rootdir", Enabled: 1},
		},
		apps: []pveclient.Appliance{
			{Template: "ubuntu-24.04-standard_24.04-2_amd64.tar.zst", Package: "ubuntu-24.04-standard"},
			{Template: "ubuntu-26.04-standard_26.04-1_amd64.tar.zst", Package: "ubuntu-26.04-standard"},
		},
		content: []pveclient.StorageContent{{Volid: "local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst"}},
		nextID:  110,
		ifaces:  []pveclient.LXCIface{{Name: "eth0", Inet: "192.168.0.37/24"}},
	}
}

func TestCreate(t *testing.T) {
	api := baseAPI()
	run := &fakeRun{}
	vmid, err := Create(context.Background(), api, run.run, CreateOptions{Node: "p0", Bridge: "vmbr0", Storage: "vm-data", Routes: []string{"192.168.0.185/32"}})
	if err != nil || vmid != 110 {
		t.Fatalf("Create = %d, %v", vmid, err)
	}
	if api.downloaded != "local:ubuntu-26.04-standard_26.04-1_amd64.tar.zst" {
		t.Errorf("downloaded = %q, want the newest LTS onto local", api.downloaded)
	}
	c := api.created
	for k, want := range map[string]string{
		"ostemplate": "local:vztmpl/ubuntu-26.04-standard_26.04-1_amd64.tar.zst", "unprivileged": "1", "features": "nesting=1",
		"rootfs": "vm-data:3", "onboot": "1", "startup": "order=1,up=10", "protection": "1", "tags": Tag, "hostname": Tag,
	} {
		if c[k] != want {
			t.Errorf("%s = %q, want %q", k, c[k], want)
		}
	}
	if !strings.Contains(c["net0"], "bridge=vmbr0") || !strings.Contains(c["net0"], "hwaddr=BC:24:11:") || !strings.Contains(c["net0"], "ip=dhcp") || strings.Contains(c["net0"], "firewall") {
		t.Errorf("net0 = %q", c["net0"])
	}
	if _, has := c["ssh-public-keys"]; has {
		t.Error("router container got SSH keys")
	}
	if len(run.commands) != 1 || !strings.Contains(run.commands[0], "pct exec 110 -- /bin/sh -c") || !strings.Contains(run.commands[0], "userspace-networking") {
		t.Errorf("setup commands = %q", run.commands)
	}
}

func TestCreateRefusesSecondRouter(t *testing.T) {
	api := baseAPI()
	api.resources = []pveclient.Resource{{VMID: 110, Type: "lxc", Tags: Tag}}
	if _, err := Create(context.Background(), api, (&fakeRun{}).run, CreateOptions{Node: "p0", Bridge: "vmbr0"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateFirewall(t *testing.T) {
	api := baseAPI()
	o := CreateOptions{Node: "p0", Bridge: "vmbr0", Routes: []string{"192.168.0.185/32"}, Firewall: true}
	if _, err := Create(context.Background(), api, (&fakeRun{}).run, o); err == nil || !strings.Contains(err.Error(), "--ip") {
		t.Fatalf("no --ip: err = %v", err)
	}
	o.IP, o.Gateway = "192.168.0.250/24", "192.168.0.1"
	if _, err := Create(context.Background(), api, (&fakeRun{}).run, o); err == nil || !strings.Contains(err.Error(), "datacenter firewall is off") {
		t.Fatalf("datacenter off: err = %v", err)
	}
	if api.created != nil {
		t.Fatal("created a container despite the refusal")
	}
	api.firewallOn = true
	if _, err := Create(context.Background(), api, (&fakeRun{}).run, o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(api.created["net0"], "ip=192.168.0.250/24,gw=192.168.0.1,firewall=1") {
		t.Errorf("net0 = %q", api.created["net0"])
	}
	if api.fwOptions.Get("policy_out") != "DROP" || api.fwOptions.Get("ipfilter") != "1" || api.fwOptions.Get("enable") != "1" {
		t.Errorf("firewall options = %v", api.fwOptions)
	}
	var toNode bool
	for _, r := range api.fwRules {
		if r.Get("type") == "out" && r.Get("dest") == "192.168.0.185/32" {
			toNode = true
		}
	}
	if !toNode {
		t.Errorf("no outbound rule to the routed host: %v", api.fwRules)
	}
}

func TestJoinKeyNeverInCommand(t *testing.T) {
	run := &fakeRun{}
	key := "tskey-auth-secret-0123456789"
	if err := Join(context.Background(), run.run, 110, "p0", []string{"192.168.0.185/32"}, []byte(key+"\n"), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run.commands[0], key) {
		t.Fatalf("the auth key is in the command line: %q", run.commands[0])
	}
	if run.stdins[0] != key {
		t.Errorf("stdin = %q, want the key", run.stdins[0])
	}
	for _, want := range []string{"--auth-key=file:/run/pmox-ts.key", "rm -f /run/pmox-ts.key", "--advertise-routes=192.168.0.185/32", "--hostname=pmox-router-p0", "--accept-dns=false", "--accept-routes=false", "--ssh=false"} {
		if !strings.Contains(run.commands[0], want) {
			t.Errorf("join command lacks %q: %s", want, run.commands[0])
		}
	}
}

func TestParseState(t *testing.T) {
	s, err := parseState(runningStateOut)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend != "Running" || s.IP != "100.81.31.119" || !s.Online {
		t.Errorf("state = %+v", s)
	}
	if !slices.Equal(s.Approved, []string{"192.168.0.185/32"}) || !slices.Equal(s.Awaiting(), []string{"192.168.0.171/32"}) {
		t.Errorf("approved = %v, awaiting = %v", s.Approved, s.Awaiting())
	}
}

func TestChangeRoutes(t *testing.T) {
	api := baseAPI()
	api.resources = []pveclient.Resource{{VMID: 110, Type: "lxc", Tags: Tag, Status: "running", Node: "p0"}}
	run := &fakeRun{stateOut: runningStateOut}
	ctx := context.Background()

	got, err := ChangeRoutes(ctx, api, run.run, []string{"192.168.0.172/32"}, nil, "192.168.0.185/32", false)
	if err != nil || !slices.Equal(got, []string{"192.168.0.185/32", "192.168.0.171/32", "192.168.0.172/32"}) {
		t.Fatalf("add: %v, %v", got, err)
	}
	if last := run.commands[len(run.commands)-1]; !strings.Contains(last, "tailscale set --advertise-routes=192.168.0.185/32,192.168.0.171/32,192.168.0.172/32") {
		t.Errorf("set command = %s", last)
	}
	if _, err := ChangeRoutes(ctx, api, run.run, nil, []string{"192.168.0.185/32"}, "192.168.0.185/32", false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("removing the node route without force: err = %v", err)
	}
	if got, err := ChangeRoutes(ctx, api, run.run, nil, []string{"192.168.0.171/32"}, "192.168.0.185/32", false); err != nil || !slices.Equal(got, []string{"192.168.0.185/32"}) {
		t.Errorf("remove: %v, %v", got, err)
	}
	if _, err := ChangeRoutes(ctx, api, run.run, nil, []string{"192.168.0.9/32"}, "", false); err == nil {
		t.Error("removing a route that isn't advertised succeeded")
	}
	if _, err := ChangeRoutes(ctx, api, run.run, []string{"8.8.8.8/32"}, nil, "", false); err == nil {
		t.Error("public route accepted")
	}
}

func TestDelete(t *testing.T) {
	api := baseAPI()
	api.resources = []pveclient.Resource{{VMID: 110, Type: "lxc", Tags: Tag, Status: "running", Node: "p0"}}
	run := &fakeRun{}
	vmid, node, logoutErr, err := Delete(context.Background(), api, run.run)
	if err != nil || logoutErr != nil || vmid != 110 || node != "p0" {
		t.Fatalf("Delete = %d %s %v %v", vmid, node, logoutErr, err)
	}
	if !strings.Contains(run.commands[0], "tailscale logout") {
		t.Errorf("first command = %q, want logout", run.commands[0])
	}
	if !slices.Equal(api.calls, []string{"setconfig", "stop", "destroy"}) || api.setConfig.Get("protection") != "0" {
		t.Errorf("calls = %v, setconfig = %v", api.calls, api.setConfig)
	}

	api.calls = nil
	failing := &fakeRun{err: errors.New("ssh down")}
	_, _, logoutErr, err = Delete(context.Background(), api, failing.run)
	if err != nil || logoutErr == nil || !slices.Contains(api.calls, "destroy") {
		t.Errorf("failed logout: logoutErr=%v err=%v calls=%v; want destroyed anyway", logoutErr, err, api.calls)
	}
}

func TestReport(t *testing.T) {
	api := baseAPI()
	api.resources = []pveclient.Resource{{VMID: 110, Type: "lxc", Tags: Tag, Status: "running", Node: "p0"}}
	api.bridges = []pveclient.Bridge{{Iface: "vmbr0", Address: "192.168.0.185"}}
	api.config = map[string]string{"onboot": "1", "startup": "order=1,up=10", "protection": "1", "net0": "name=eth0,bridge=vmbr0,ip=dhcp"}
	st, err := Report(context.Background(), api, (&fakeRun{stateOut: runningStateOut}).run, StatusOptions{Bridge: "vmbr0"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.OnBoot || !st.Protected || st.Egress != "unrestricted" || st.NodeRoute != "192.168.0.185/32" || !st.RouteFresh || st.Address != "192.168.0.37/24" {
		t.Errorf("status = %+v", st)
	}
	if st.Tailscale == nil || st.Tailscale.Backend != "Running" {
		t.Errorf("tailscale = %+v (%s)", st.Tailscale, st.TailscaleErr)
	}
}
