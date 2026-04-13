package launch

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

	f.srv.Handle("GET", "/nodes/pve/storage", func(w http.ResponseWriter, _ *http.Request, _ string) {
		fmt.Fprint(w, `{"data":[{"storage":"local-lvm","content":"snippets,images","active":1,"enabled":1}]}`)
	})

	f.srv.Handle("POST", "/storage/local-lvm/upload", func(w http.ResponseWriter, _ *http.Request, _ string) {
		fmt.Fprint(w, `{"data":null}`)
	})

	return f
}

func (f *launchFake) client() *pveclient.Client { return f.srv.Client() }

func (f *launchFake) orderedPaths() []string { return f.srv.OrderedPaths() }

func (f *launchFake) configBodies() []string { return f.srv.Bodies("POST", "/config") }

// writeCI writes a valid cloud-init file (with an ssh_authorized_keys
// stanza so warnings don't fire) to a tempdir and returns the path.
func writeCI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cloud-init.yaml")
	content := []byte("#cloud-config\nusers:\n  - name: ubuntu\n    ssh_authorized_keys:\n      - ssh-ed25519 AAAA test\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func baseOpts(t *testing.T, c *pveclient.Client) Options {
	return Options{
		Client:        c,
		Node:          "pve",
		Name:          "web1",
		TemplateID:    9000,
		CPU:           2,
		MemMB:         2048,
		DiskSize:      "20G",
		Storage:       "local-lvm",
		Wait:          5 * time.Second,
		NoWaitSSH:     true,
		CloudInitPath: writeCI(t),
	}
}

func TestRun_HappyPath(t *testing.T) {
	f := newLaunchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := Run(ctx, baseOpts(t, f.client()))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if r == nil || r.VMID != 101 || r.IP != "10.9.8.7" {
		t.Fatalf("Result = %+v, want VMID=101 IP=10.9.8.7", r)
	}

	paths := f.orderedPaths()
	var uploadIdx, secondCfgIdx = -1, -1
	cfgSeen := 0
	for i, p := range paths {
		if strings.Contains(p, "/storage/local-lvm/upload") {
			uploadIdx = i
		}
		if strings.HasSuffix(p, "/config") && strings.HasPrefix(p, "POST ") {
			cfgSeen++
			if cfgSeen == 2 {
				secondCfgIdx = i
			}
		}
	}
	if uploadIdx == -1 {
		t.Fatalf("upload not called: %v", paths)
	}
	if secondCfgIdx == -1 || uploadIdx >= secondCfgIdx {
		t.Errorf("upload (idx %d) must precede full SetConfig (idx %d)", uploadIdx, secondCfgIdx)
	}

	bodies := f.configBodies()
	if len(bodies) < 2 {
		t.Fatalf("want 2 config bodies, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "tags=pmox") {
		t.Errorf("first config body = %q, want tags=pmox", bodies[0])
	}
	parsed, _ := url.ParseQuery(bodies[1])
	wantCi := "user=local-lvm:snippets/pmox-101-user-data.yaml"
	if got := parsed.Get("cicustom"); got != wantCi {
		t.Errorf("cicustom = %q, want %q", got, wantCi)
	}
	if parsed.Get("ciuser") != "" {
		t.Errorf("ciuser must not appear: %q", parsed.Get("ciuser"))
	}
	if parsed.Get("sshkeys") != "" {
		t.Errorf("sshkeys must not appear: %q", parsed.Get("sshkeys"))
	}
	for _, k := range []string{"name", "memory", "cores", "agent", "ipconfig0", "cicustom", "ide2"} {
		if parsed.Get(k) == "" {
			t.Errorf("config body missing %q: %v", k, parsed)
		}
	}
}

func TestRun_TagBeforeResize(t *testing.T) {
	f := newLaunchFake(t)
	_, err := Run(context.Background(), baseOpts(t, f.client()))
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
	_, err := Run(context.Background(), baseOpts(t, f.client()))
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
	_, err := Run(context.Background(), baseOpts(t, f.client()))
	if err == nil {
		t.Fatal("Run err=nil, want start failure")
	}
	if atomic.LoadInt32(&f.deleteHit) != 0 {
		t.Errorf("Run issued %d DELETE calls, want 0 (no auto-rollback)", f.deleteHit)
	}
}

func TestRun_MissingCloudInitFile(t *testing.T) {
	f := newLaunchFake(t)
	opts := baseOpts(t, f.client())
	opts.CloudInitPath = filepath.Join(t.TempDir(), "nope.yaml")
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected missing-file error")
	}
	if !strings.Contains(err.Error(), "pmox configure --regen-cloud-init") {
		t.Errorf("err = %v, want regen hint", err)
	}
	if len(f.orderedPaths()) != 0 {
		t.Errorf("expected 0 PVE calls, got %v", f.orderedPaths())
	}
}

func TestRun_InvalidCloudInitFile(t *testing.T) {
	f := newLaunchFake(t)
	dir := t.TempDir()
	ciPath := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(ciPath, []byte{0xff, 0xfe, 0xfd}, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := baseOpts(t, f.client())
	opts.CloudInitPath = ciPath

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Errorf("err = %v, want UTF-8 message", err)
	}
	if len(f.orderedPaths()) != 0 {
		t.Errorf("expected 0 PVE calls, got %v", f.orderedPaths())
	}
}

func TestRun_SSHWarning(t *testing.T) {
	f := newLaunchFake(t)
	dir := t.TempDir()
	ciPath := filepath.Join(dir, "user-data.yaml")
	if err := os.WriteFile(ciPath, []byte("#cloud-config\nhostname: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	opts := baseOpts(t, f.client())
	opts.CloudInitPath = ciPath
	opts.Stderr = &stderr

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !strings.Contains(stderr.String(), "ssh_authorized_keys") {
		t.Errorf("stderr = %q, want ssh warning", stderr.String())
	}
}

func TestRun_WaitIPTimeout(t *testing.T) {
	f := newLaunchFake(t)
	f.agentTimeout = true
	opts := baseOpts(t, f.client())
	opts.Wait = 1500 * time.Millisecond
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want IP wait timeout")
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent not responding on VM") {
		t.Errorf("err = %v, want qemu-guest-agent message", err)
	}
}
