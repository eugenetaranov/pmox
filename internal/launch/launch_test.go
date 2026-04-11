package launch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// fakePVE is a stateful httptest-backed PVE stub. It records every
// request and dispatches by URL path, tracking enough state to walk
// the launch state machine end-to-end without a real cluster.
type fakePVE struct {
	t         *testing.T
	srv       *httptest.Server
	client    *pveclient.Client
	mu        sync.Mutex
	hits      []hit
	agentHits int32
	deleteHit int32
	// Behavior toggles for failure-path tests.
	failTagCfg   bool
	failStart    bool
	agentTimeout bool
}

type hit struct {
	method string
	path   string
	body   string
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	f := &fakePVE{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.client = pveclient.New(f.srv.URL, "tok@pam!x", "secret", false)
	f.client.HTTPClient = f.srv.Client()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePVE) record(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	s := string(body)
	f.mu.Lock()
	f.hits = append(f.hits, hit{method: r.Method, path: r.URL.Path, body: s})
	f.mu.Unlock()
	return s
}

// orderedPaths returns path fragments describing each hit in order,
// using short labels so tests can assert against a sequence.
func (f *fakePVE) orderedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.hits))
	for _, h := range f.hits {
		label := h.method + " " + h.path
		out = append(out, label)
	}
	return out
}

// configBodies returns every POST config body in order.
func (f *fakePVE) configBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, h := range f.hits {
		if h.method == "POST" && strings.HasSuffix(h.path, "/config") {
			out = append(out, h.body)
		}
	}
	return out
}

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	body := f.record(r)
	p := r.URL.Path

	switch {
	case r.Method == "GET" && strings.HasSuffix(p, "/cluster/nextid"):
		fmt.Fprint(w, `{"data":"101"}`)

	case r.Method == "POST" && strings.Contains(p, "/qemu/") && strings.HasSuffix(p, "/clone"):
		fmt.Fprint(w, `{"data":"UPID:pve:clone"}`)

	case r.Method == "POST" && strings.Contains(p, "/qemu/") && strings.HasSuffix(p, "/status/start"):
		if f.failStart {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"start failed"}`)
			return
		}
		fmt.Fprint(w, `{"data":"UPID:pve:start"}`)

	case r.Method == "GET" && strings.Contains(p, "/tasks/") && strings.HasSuffix(p, "/status"):
		fmt.Fprint(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)

	case r.Method == "POST" && strings.HasSuffix(p, "/config"):
		// First /config call is the tag; the second is the full kv map.
		if f.failTagCfg && strings.Contains(body, "tags=pmox") {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"cannot set tags"}`)
			return
		}
		fmt.Fprint(w, `{"data":null}`)

	case r.Method == "PUT" && strings.HasSuffix(p, "/resize"):
		fmt.Fprint(w, `{"data":null}`)

	case r.Method == "GET" && strings.HasSuffix(p, "/agent/network-get-interfaces"):
		n := atomic.AddInt32(&f.agentHits, 1)
		if f.agentTimeout {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"agent not running"}`)
			return
		}
		// Return agent-not-running once, then a real interface.
		if n < 2 {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"errors":"agent not running"}`)
			return
		}
		fmt.Fprint(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"10.9.8.7"}]}]}}`)

	case r.Method == "DELETE" && strings.Contains(p, "/qemu/"):
		atomic.AddInt32(&f.deleteHit, 1)
		fmt.Fprint(w, `{"data":"UPID:pve:delete"}`)

	default:
		http.NotFound(w, r)
	}
}

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
		Wait:       5 * time.Second,
		NoWaitSSH:  true,
	}
}

func TestRun_HappyPath(t *testing.T) {
	f := newFakePVE(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := Run(ctx, baseOpts(f.client))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if r == nil || r.VMID != 101 || r.IP != "10.9.8.7" {
		t.Fatalf("Result = %+v, want VMID=101 IP=10.9.8.7", r)
	}

	// Expected call sequence (a prefix-match is enough because we
	// allow repeated task-status polls and agent polls).
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

	// The first config body must be the tag-only call.
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

	// The second config body must carry the cloud-init kv map.
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
	f := newFakePVE(t)
	_, err := Run(context.Background(), baseOpts(f.client))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	// Find the index of the first /config call and the first /resize call.
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
	f := newFakePVE(t)
	f.failTagCfg = true
	_, err := Run(context.Background(), baseOpts(f.client))
	if err == nil {
		t.Fatal("Run err=nil, want tag failure error")
	}
	if !strings.Contains(err.Error(), "run pmox delete") {
		t.Errorf("err = %v, want mention of `run pmox delete`", err)
	}
}

func TestRun_StartFailsNoRollback(t *testing.T) {
	f := newFakePVE(t)
	f.failStart = true
	_, err := Run(context.Background(), baseOpts(f.client))
	if err == nil {
		t.Fatal("Run err=nil, want start failure")
	}
	if atomic.LoadInt32(&f.deleteHit) != 0 {
		t.Errorf("Run issued %d DELETE calls, want 0 (no auto-rollback)", f.deleteHit)
	}
}

func TestRun_WaitIPTimeout(t *testing.T) {
	f := newFakePVE(t)
	f.agentTimeout = true
	opts := baseOpts(f.client)
	opts.Wait = 1500 * time.Millisecond
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want IP wait timeout")
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent not responding on VM") {
		t.Errorf("err = %v, want qemu-guest-agent message", err)
	}
}
