package launch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/hook"
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

	// Cluster-wide storage record for GetStoragePath. Returns a fake
	// on-disk path so the SFTP upload phase has somewhere to target.
	f.srv.Handle("GET", "/storage/local-lvm", func(w http.ResponseWriter, _ *http.Request, _ string) {
		fmt.Fprint(w, `{"data":{"path":"/var/lib/vz"}}`)
	})

	return f
}

// stubUpload is an in-memory UploadSnippet that records the args. The
// launch state-machine tests only care that it was called with the
// resolved storage path; SFTP integration is covered in pvessh tests.
type stubUpload struct {
	storagePath string
	filename    string
	content     []byte
	err         error
	called      int32
}

func (s *stubUpload) fn(_ context.Context, storagePath, filename string, content []byte) error {
	atomic.AddInt32(&s.called, 1)
	s.storagePath = storagePath
	s.filename = filename
	s.content = append([]byte(nil), content...)
	return s.err
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

func baseOpts(t *testing.T, c *pveclient.Client) (Options, *stubUpload) {
	stub := &stubUpload{}
	return Options{
		Client:         c,
		Node:           "pve",
		Name:           "web1",
		TemplateID:     9000,
		CPU:            2,
		MemMB:          2048,
		DiskSize:       "20G",
		Storage:        "local-lvm",
		SnippetStorage: "local-lvm",
		Wait:           5 * time.Second,
		NoWaitSSH:      true,
		CloudInitPath:  writeCI(t),
		UploadSnippet:  stub.fn,
	}, stub
}

func TestRun_HappyPath(t *testing.T) {
	f := newLaunchFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts, stub := baseOpts(t, f.client())
	r, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if r == nil || r.VMID != 101 || r.IP != "10.9.8.7" {
		t.Fatalf("Result = %+v, want VMID=101 IP=10.9.8.7", r)
	}

	if atomic.LoadInt32(&stub.called) != 1 {
		t.Fatalf("UploadSnippet calls = %d, want 1", stub.called)
	}
	if stub.storagePath != "/var/lib/vz" {
		t.Errorf("upload storagePath = %q, want /var/lib/vz", stub.storagePath)
	}
	if stub.filename != "pmox-101-user-data.yaml" {
		t.Errorf("upload filename = %q, want pmox-101-user-data.yaml", stub.filename)
	}
	if !bytes.Contains(stub.content, []byte("#cloud-config")) {
		t.Errorf("upload content missing #cloud-config: %q", stub.content)
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
	opts, _ := baseOpts(t, f.client())
	_, err := Run(context.Background(), opts)
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
	opts, _ := baseOpts(t, f.client())
	_, err := Run(context.Background(), opts)
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
	opts, _ := baseOpts(t, f.client())
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want start failure")
	}
	if atomic.LoadInt32(&f.deleteHit) != 0 {
		t.Errorf("Run issued %d DELETE calls, want 0 (no auto-rollback)", f.deleteHit)
	}
}

func TestRun_MissingCloudInitFile(t *testing.T) {
	f := newLaunchFake(t)
	opts, _ := baseOpts(t, f.client())
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
	opts, _ := baseOpts(t, f.client())
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
	opts, _ := baseOpts(t, f.client())
	opts.CloudInitPath = ciPath
	opts.Stderr = &stderr

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !strings.Contains(stderr.String(), "ssh_authorized_keys") {
		t.Errorf("stderr = %q, want ssh warning", stderr.String())
	}
}

// fakeWaitSSH returns a WaitForSSHFn that always succeeds immediately.
// Used by hook-phase tests so they don't need a live :22 endpoint.
func fakeWaitSSH() func(ctx context.Context, ip string, timeout time.Duration) error {
	return func(_ context.Context, _ string, _ time.Duration) error { return nil }
}

func writeHookScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_HookSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook not supported on windows")
	}
	f := newLaunchFake(t)
	markerDir := t.TempDir()
	markerPath := filepath.Join(markerDir, "marker")
	script := writeHookScript(t, "touch "+markerPath)

	opts, _ := baseOpts(t, f.client())
	opts.NoWaitSSH = false
	opts.WaitForSSHFn = fakeWaitSSH()
	opts.Hook = &hook.PostCreateHook{Path: script}
	opts.User = "ubuntu"

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("marker file %s missing: %v", markerPath, err)
	}
}

func TestRun_HookFailureLenient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook not supported on windows")
	}
	f := newLaunchFake(t)
	script := writeHookScript(t, "exit 1")

	var stderr bytes.Buffer
	opts, _ := baseOpts(t, f.client())
	opts.NoWaitSSH = false
	opts.WaitForSSHFn = fakeWaitSSH()
	opts.Hook = &hook.PostCreateHook{Path: script}
	opts.StrictHooks = false
	opts.Stderr = &stderr

	_, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run err: %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "post-create hook failed") {
		t.Errorf("stderr = %q, want 'post-create hook failed'", stderr.String())
	}
}

func TestRun_HookFailureStrict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook not supported on windows")
	}
	f := newLaunchFake(t)
	script := writeHookScript(t, "exit 1")

	var stderr bytes.Buffer
	opts, _ := baseOpts(t, f.client())
	opts.NoWaitSSH = false
	opts.WaitForSSHFn = fakeWaitSSH()
	opts.Hook = &hook.PostCreateHook{Path: script}
	opts.StrictHooks = true
	opts.Stderr = &stderr

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want *HookError")
	}
	var hookErr *HookError
	if !errors.As(err, &hookErr) {
		t.Errorf("err = %T (%v), want *HookError", err, err)
	}
	if atomic.LoadInt32(&f.deleteHit) != 0 {
		t.Errorf("strict hook failure issued %d DELETE calls, want 0", f.deleteHit)
	}
}

func TestRun_HookSkippedOnNoWaitSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook not supported on windows")
	}
	f := newLaunchFake(t)
	markerDir := t.TempDir()
	markerPath := filepath.Join(markerDir, "marker")
	script := writeHookScript(t, "touch "+markerPath)

	var stderr bytes.Buffer
	opts, _ := baseOpts(t, f.client())
	opts.NoWaitSSH = true
	opts.Hook = &hook.PostCreateHook{Path: script}
	opts.Stderr = &stderr

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if _, err := os.Stat(markerPath); err == nil {
		t.Errorf("marker file %s exists; hook should have been skipped", markerPath)
	}
	if !strings.Contains(stderr.String(), "--no-wait-ssh set; hook will not run") {
		t.Errorf("stderr = %q, want skip warning", stderr.String())
	}
}

// deadlineHook records the context deadline it was invoked with.
type deadlineHook struct {
	deadline time.Time
	hasDL    bool
}

func (h *deadlineHook) Name() string { return "deadline" }

func (h *deadlineHook) Run(ctx context.Context, _ hook.Env, _, _ io.Writer) error {
	h.deadline, h.hasDL = ctx.Deadline()
	return nil
}

func TestRun_HookTimeoutFloor(t *testing.T) {
	f := newLaunchFake(t)
	dh := &deadlineHook{}
	opts, _ := baseOpts(t, f.client())
	opts.NoWaitSSH = false
	opts.WaitForSSHFn = fakeWaitSSH()
	opts.Hook = dh
	// A tiny Wait budget simulates wait-IP + wait-SSH consuming
	// nearly everything. The floor should kick in and grant >=30s.
	opts.Wait = 100 * time.Millisecond

	start := time.Now()
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !dh.hasDL {
		t.Fatal("hook ctx had no deadline; want one derived from hook budget")
	}
	remaining := time.Until(dh.deadline)
	// time.Until is measured at the assertion point, not at hook
	// invocation — subtract a small slack to allow for the elapsed
	// time between hook invocation and this check.
	elapsed := time.Since(start)
	grantedAtInvocation := remaining + elapsed
	if grantedAtInvocation < 30*time.Second {
		t.Errorf("hook deadline granted = %v, want >=30s floor", grantedAtInvocation)
	}
}

func TestRun_WaitIPTimeout(t *testing.T) {
	f := newLaunchFake(t)
	f.agentTimeout = true
	opts, _ := baseOpts(t, f.client())
	opts.Wait = 1500 * time.Millisecond
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run err=nil, want IP wait timeout")
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent not responding on VM") {
		t.Errorf("err = %v, want qemu-guest-agent message", err)
	}
}
