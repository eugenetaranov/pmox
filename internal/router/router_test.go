package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestPickTemplate(t *testing.T) {
	app := func(tmpl string) pveclient.Appliance {
		pkg, _, _ := strings.Cut(tmpl, "_")
		return pveclient.Appliance{Template: tmpl, Package: pkg, Section: "system"}
	}
	cases := []struct {
		name string
		apps []pveclient.Appliance
		want string
	}{
		{"newest LTS, not interim", []pveclient.Appliance{
			app("ubuntu-22.04-standard_22.04-1_amd64.tar.zst"),
			app("ubuntu-24.04-standard_24.04-2_amd64.tar.zst"),
			app("ubuntu-25.10-standard_25.10-1_amd64.tar.zst"),
			app("ubuntu-26.04-standard_26.04-1_amd64.tar.zst"),
			app("ubuntu-25.04-standard_25.04-1.1_amd64.tar.zst"),
			app("debian-13-standard_13.1-2_amd64.tar.zst"),
		}, "ubuntu-26.04-standard_26.04-1_amd64.tar.zst"},
		{"odd YY.04 is not LTS", []pveclient.Appliance{
			app("ubuntu-24.04-standard_24.04-2_amd64.tar.zst"),
			app("ubuntu-25.04-standard_25.04-1.1_amd64.tar.zst"),
		}, "ubuntu-24.04-standard_24.04-2_amd64.tar.zst"},
		{"no Ubuntu: newest Debian", []pveclient.Appliance{
			app("debian-12-standard_12.12-1_amd64.tar.zst"),
			app("debian-13-standard_13.1-1_amd64.tar.zst"),
			app("debian-13-standard_13.1-2_amd64.tar.zst"),
			app("alpine-3.22-default_20250617_amd64.tar.xz"),
		}, "debian-13-standard_13.1-2_amd64.tar.zst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PickTemplate(tc.apps)
			if !ok || got.Template != tc.want {
				t.Errorf("PickTemplate = %q, %v; want %q", got.Template, ok, tc.want)
			}
		})
	}
	if _, ok := PickTemplate([]pveclient.Appliance{app("alpine-3.22-default_20250617_amd64.tar.xz")}); ok {
		t.Error("PickTemplate found a template in an index without Ubuntu or Debian")
	}
}

func TestCheckRoute(t *testing.T) {
	ok := []string{"192.168.0.185/32", "192.168.0.0/24", "10.83.16.0/23", "172.16.5.0/24", "10.1.0.0/16"}
	bad := map[string]string{
		"192.0.0.0/3":      "private",
		"192.168.0.185/24": "host bits",
		"8.8.8.8/32":       "private",
		"10.0.0.0/8":       "wider than /16",
		"100.64.0.0/10":    "private",
		"192.168.0.185":    "IPv4 CIDR",
		"fd00::/64":        "IPv4 CIDR",
	}
	for _, r := range ok {
		if err := CheckRoute(r); err != nil {
			t.Errorf("CheckRoute(%q) = %v, want ok", r, err)
		}
	}
	for r, want := range bad {
		if err := CheckRoute(r); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckRoute(%q) = %v, want an error about %q", r, err, want)
		}
	}
}

type fakeBridges struct {
	API
	bridges []pveclient.Bridge
}

func (f fakeBridges) ListBridges(context.Context, string) ([]pveclient.Bridge, error) {
	return f.bridges, nil
}

func TestNodeRoute(t *testing.T) {
	ctx := context.Background()
	withAddr := fakeBridges{bridges: []pveclient.Bridge{{Iface: "vmbr0", Address: "192.168.0.185"}}}
	if got, err := NodeRoute(ctx, withAddr, "p0", "vmbr0", "https://pve.example:8006", nil); err != nil || got != "192.168.0.185/32" {
		t.Errorf("from bridge: %q, %v", got, err)
	}
	none := fakeBridges{}
	if got, err := NodeRoute(ctx, none, "p0", "vmbr0", "https://192.168.0.185:8006/api2/json", nil); err != nil || got != "192.168.0.185/32" {
		t.Errorf("from URL: %q, %v", got, err)
	}
	overlay := func(context.Context, string) ([]string, error) { return []string{"100.101.2.3"}, nil }
	if _, err := NodeRoute(ctx, none, "p0", "vmbr0", "https://p0.tailnet.ts.net:8006", overlay); err == nil || !strings.Contains(err.Error(), "--route") {
		t.Errorf("overlay host: err = %v, want a --route hint", err)
	}
	if _, err := NodeRoute(ctx, none, "p0", "vmbr0", "https://203.0.113.5:8006", nil); err == nil {
		t.Error("public host accepted")
	}
}

type fakeResources struct {
	API
	res []pveclient.Resource
}

func (f fakeResources) ClusterResources(context.Context, string) ([]pveclient.Resource, error) {
	return f.res, nil
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	vm := pveclient.Resource{VMID: 104, Type: "qemu", Tags: "pmox"}
	taggedVM := pveclient.Resource{VMID: 105, Type: "qemu", Tags: "pmox-router"}
	router := pveclient.Resource{VMID: 110, Type: "lxc", Tags: "pmox-router"}
	if _, err := Find(ctx, fakeResources{res: []pveclient.Resource{vm, taggedVM}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no router: err = %v", err)
	}
	if r, err := Find(ctx, fakeResources{res: []pveclient.Resource{vm, router}}); err != nil || r.VMID != 110 {
		t.Errorf("one router: %+v, %v", r, err)
	}
	two := router
	two.VMID = 111
	if _, err := Find(ctx, fakeResources{res: []pveclient.Resource{router, two}}); err == nil || !strings.Contains(err.Error(), "110, 111") {
		t.Errorf("two routers: err = %v", err)
	}
}
