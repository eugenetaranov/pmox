package pveclient

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// recordingClient answers every request with body and records the last
// method, path, query and form.
func recordingClient(t *testing.T, body string) (*Client, *http.Request, *url.Values) {
	t.Helper()
	var last http.Request
	var form url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		last = *r
		_, _ = w.Write([]byte(body))
	})
	return c, &last, &form
}

func TestCreateLXC(t *testing.T) {
	c, req, form := recordingClient(t, `{"data":"UPID:p0:1:create"}`)
	upid, err := c.CreateLXC(context.Background(), "p0", 110, map[string]string{"hostname": "pmox-router", "features": "nesting=1"})
	if err != nil || upid != "UPID:p0:1:create" {
		t.Fatalf("CreateLXC = %q, %v", upid, err)
	}
	if req.Method != "POST" || req.URL.Path != "/nodes/p0/lxc" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if form.Get("vmid") != "110" || form.Get("features") != "nesting=1" || form.Get("hostname") != "pmox-router" {
		t.Errorf("form = %v", *form)
	}
}

func TestDeleteLXCPurges(t *testing.T) {
	c, req, _ := recordingClient(t, `{"data":"UPID:p0:1:destroy"}`)
	if _, err := c.DeleteLXC(context.Background(), "p0", 110); err != nil {
		t.Fatal(err)
	}
	if req.Method != "DELETE" || req.URL.Path != "/nodes/p0/lxc/110" ||
		req.URL.Query().Get("purge") != "1" || req.URL.Query().Get("destroy-unreferenced-disks") != "1" {
		t.Errorf("request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
	}
}

func TestLXCInterfaces(t *testing.T) {
	c, req, _ := recordingClient(t, `{"data":[{"name":"lo","inet":"127.0.0.1/8"},{"name":"eth0","inet":"192.168.0.37/24","hwaddr":"bc:24:11:5a:1e:01"}]}`)
	got, err := c.LXCInterfaces(context.Background(), "p0", 110)
	if err != nil || len(got) != 2 || got[1].Name != "eth0" || got[1].Inet != "192.168.0.37/24" {
		t.Fatalf("LXCInterfaces = %+v, %v", got, err)
	}
	if req.URL.Path != "/nodes/p0/lxc/110/interfaces" {
		t.Errorf("path = %s", req.URL.Path)
	}
}

func TestAplInfoAndDownload(t *testing.T) {
	c, _, _ := recordingClient(t, `{"data":[{"template":"ubuntu-24.04-standard_24.04-2_amd64.tar.zst","package":"ubuntu-24.04-standard","section":"system","os":"ubuntu-24.04"}]}`)
	apps, err := c.AplInfo(context.Background(), "p0")
	if err != nil || len(apps) != 1 || apps[0].Package != "ubuntu-24.04-standard" {
		t.Fatalf("AplInfo = %+v, %v", apps, err)
	}
	c, req, form := recordingClient(t, `{"data":"UPID:p0:1:download"}`)
	if _, err := c.DownloadAppliance(context.Background(), "p0", "local", "ubuntu-26.04-standard_26.04-1_amd64.tar.zst"); err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" || req.URL.Path != "/nodes/p0/aplinfo" || form.Get("storage") != "local" || form.Get("template") != "ubuntu-26.04-standard_26.04-1_amd64.tar.zst" {
		t.Errorf("request = %s %s %v", req.Method, req.URL.Path, *form)
	}
}

func TestClusterFirewallEnabled(t *testing.T) {
	for body, want := range map[string]bool{
		`{"data":{"enable":1,"policy_in":"DROP"}}`: true,
		`{"data":{"policy_in":"DROP"}}`:            false,
		`{"data":{"enable":"0"}}`:                  false,
	} {
		c, _, _ := recordingClient(t, body)
		got, err := c.ClusterFirewallEnabled(context.Background())
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", body, got, err, want)
		}
	}
}

func TestBridgeAddress(t *testing.T) {
	c, _, _ := recordingClient(t, `{"data":[{"iface":"vmbr0","type":"bridge","address":"192.168.0.185","cidr":"192.168.0.185/24"}]}`)
	got, err := c.ListBridges(context.Background(), "p0")
	if err != nil || len(got) != 1 || got[0].Address != "192.168.0.185" || got[0].CIDR != "192.168.0.185/24" {
		t.Fatalf("ListBridges = %+v, %v", got, err)
	}
}
