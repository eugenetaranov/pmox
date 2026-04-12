package launch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvetest"
)

// launchFake wraps a pvetest.Server with the behavior-toggle flags the
// launch state-machine tests need. It centralizes the PVE route wiring
// so each test just flips the failure flags it cares about.
type launchFake struct {
	srv *pvetest.Server

	agentHits int32
	deleteHit int32

	failTagCfg   bool
	failStart    bool
	agentTimeout bool
}

func newLaunchFake(t *testing.T) *launchFake {
	t.Helper()
	f := &launchFake{srv: pvetest.New(t)}

	f.srv.Handle("GET", "/cluster/nextid", pvetest.JSON(`{"data":"101"}`))

	f.srv.Handle("POST", "/clone", pvetest.JSON(`{"data":"UPID:pve:clone"}`))

	f.srv.Handle("POST", "/status/start", func(w http.ResponseWriter, _ *http.Request, _ string) {
		if f.failStart {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"start failed"}`)
			return
		}
		fmt.Fprint(w, `{"data":"UPID:pve:start"}`)
	})

	f.srv.Handle("GET", "/tasks/", pvetest.TaskOK)

	f.srv.Handle("POST", "/config", func(w http.ResponseWriter, _ *http.Request, body string) {
		if f.failTagCfg && strings.Contains(body, "tags=pmox") {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"cannot set tags"}`)
			return
		}
		fmt.Fprint(w, `{"data":null}`)
	})

	f.srv.Handle("PUT", "/resize", pvetest.JSON(`{"data":null}`))

	f.srv.Handle("GET", "/agent/network-get-interfaces", func(w http.ResponseWriter, _ *http.Request, _ string) {
		n := atomic.AddInt32(&f.agentHits, 1)
		if f.agentTimeout {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"agent not running"}`)
			return
		}
		if n < 2 {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"agent not running"}`)
			return
		}
		fmt.Fprint(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"10.9.8.7"}]}]}}`)
	})

	f.srv.Handle("DELETE", "/qemu/", func(w http.ResponseWriter, _ *http.Request, _ string) {
		atomic.AddInt32(&f.deleteHit, 1)
		fmt.Fprint(w, `{"data":"UPID:pve:delete"}`)
	})

	return f
}

func (f *launchFake) client() *pveclient.Client { return f.srv.Client() }

// orderedPaths proxies through to the underlying server, keeping the
// existing test assertions unchanged.
func (f *launchFake) orderedPaths() []string { return f.srv.OrderedPaths() }

// configBodies returns every POST /config body in order.
func (f *launchFake) configBodies() []string { return f.srv.Bodies("POST", "/config") }

func baseOpts(c *pveclient.Client) Options {
	return Options{
		Client:     c,
		Node:       "pve",
		Name:       "web1",
		User:       "pmox",
		SSHPubKey:  "ssh-ed25519 AAAA test@host",
		TemplateID: 9000,
		CPU:        2,
		MemMB:      2048,
		DiskSize:   "20G",
		Storage:    "local-lvm",
		Wait:       5 * time.Second,
		NoWaitSSH:  true,
	}
}

func TestRun_HappyPath(t *testing.T) {
	f := newLaunchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := Run(ctx, baseOpts(f.client()))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if r == nil || r.VMID != 101 || r.IP != "10.9.8.7" {
		t.Fatalf("Result = %+v, want VMID=101 IP=10.9.8.7", r)
	}

	expected := []string{
		"GET /cluster/nextid",
		"POST /nodes/pve/qemu/9000/clone",
		"GET /nodes/pve/tasks/UPID:pve:clone/status",
		"POST /nodes/pve/qemu/101/config", // tag
		"PUT /nodes/pve/qemu/101/resize",
		"POST /nodes/pve/qemu/101/config", // full kv
		"POST /nodes/pve/qemu/101/status/start",
		"GET /nodes/pve/tasks/UPID:pve:start/status",
	}
	got := f.orderedPaths()
	if len(got) < len(expected) {
		t.Fatalf("got %d hits, want at least %d: %v", len(got), len(expected), got)
	}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("hit[%d] = %q, want %q", i, got[i], want)
		}
	}

	bodies := f.configBodies()
	if len(bodies) < 2 {
		t.Fatalf("want 2 config bodies, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "tags=pmox") {
		t.Errorf("first config body = %q, want tags=pmox", bodies[0])
	}
	if strings.Contains(bodies[0], "ciuser") {
		t.Error("first config body should be tag-only, not include ciuser")
	}

	parsed, _ := url.ParseQuery(bodies[1])
	for _, k := range []string{"ciuser", "sshkeys", "ipconfig0", "agent", "memory", "cores", "name"} {
		if parsed.Get(k) == "" {
			t.Errorf("second config body missing %q: %v", k, parsed)
		}
	}
	if parsed.Get("ipconfig0") != "ip=dhcp" {
		t.Errorf("ipconfig0 = %q, want ip=dhcp", parsed.Get("ipconfig0"))
	}
	if parsed.Get("agent") != "1" {
		t.Errorf("agent = %q, want 1", parsed.Get("agent"))
	}
}

func TestRun_TagBeforeResize(t *testing.T) {
	f := newLaunchFake(t)
	_, err := Run(context.Background(), baseOpts(f.client()))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	var tagIdx, resizeIdx = -1, -1
	for i, h := range f.orderedPaths() {
		if tagIdx == -1 && strings.HasSuffix(h, "/config") {
			tagIdx = i
		}
		if resizeIdx == -1 && strings.HasSuffix(h, "/resize") {
			resizeIdx = i
		}
	}
	if tagIdx == -1 || resizeIdx == -1 {
		t.Fatalf("missing tag or resize call: %v", f.orderedPaths())
	}
	if tagIdx >= resizeIdx {
		t.Errorf("tag (idx %d) must precede resize (idx %d)", tagIdx, resizeIdx)
	}
}

func TestRun_TagErrorMentionsCleanup(t *testing.T) {
	f := newLaunchFake(t)
	f.failTagCfg = true
	_, err := Run(context.Background(), baseOpts(f.client()))
	if err == nil {
		t.Fatal("Run err=nil, want tag failure error")
	}
	if !strings.Contains(err.Error(), "run pmox delete") {
		t.Errorf("err = %v, want mention of `run pmox delete`", err)
	}
}

func TestRun_StartFailsNoRollback(t *testing.T) {
	f := newLaunchFake(t)
	f.failStart = true
	_, err := Run(context.Background(), baseOpts(f.client()))
	if err == nil {
		t.Fatal("Run err=nil, want start failure")
	}
	if atomic.LoadInt32(&f.deleteHit) != 0 {
		t.Errorf("Run issued %d DELETE calls, want 0 (no auto-rollback)", f.deleteHit)
	}
}

func TestRun_WaitIPTimeout(t *testing.T) {
	f := newLaunchFake(t)
	f.agentTimeout = true
	opts := baseOpts(f.client())
	opts.Wait = 1500 * time.Millisecond
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want IP wait timeout")
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent not responding on VM") {
		t.Errorf("err = %v, want qemu-guest-agent message", err)
	}
}
