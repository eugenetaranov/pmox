package pveclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- requestForm tests ---

func TestRequestForm_PostSetsContentType(t *testing.T) {
	var gotCT, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	form := url.Values{}
	form.Set("foo", "bar")
	form.Set("n", "42")
	_, err := c.requestForm(context.Background(), "POST", "/t", form)
	if err != nil {
		t.Fatalf("requestForm: %v", err)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if !strings.Contains(gotBody, "foo=bar") || !strings.Contains(gotBody, "n=42") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestRequestForm_EmptyFormNoContentType(t *testing.T) {
	var gotCT string
	var gotLen int64
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotLen = r.ContentLength
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	_, err := c.requestForm(context.Background(), "POST", "/t", nil)
	if err != nil {
		t.Fatalf("requestForm: %v", err)
	}
	if gotCT != "" {
		t.Errorf("Content-Type = %q, want empty", gotCT)
	}
	if gotLen > 0 {
		t.Errorf("body length = %d, want 0", gotLen)
	}
}

// --- NextID ---

func TestNextID_HappyPath(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cluster/nextid" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":"100"}`))
	})
	n, err := c.NextID(context.Background())
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if n != 100 {
		t.Errorf("NextID = %d", n)
	}
}

func TestNextID_NotANumber(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":"not-a-number"}`))
	})
	if _, err := c.NextID(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// --- Clone ---

func TestClone_HappyPath(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:00001234:00005678:680ABCD0:qmclone:100:root@pam:"}`))
	})
	upid, err := c.Clone(context.Background(), "pve1", 9000, 100, "test")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/nodes/pve1/qemu/9000/clone" {
		t.Errorf("path = %q", gotPath)
	}
	for _, s := range []string{"newid=100", "name=test", "full=1"} {
		if !strings.Contains(gotBody, s) {
			t.Errorf("body %q missing %q", gotBody, s)
		}
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

func TestClone_ServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Clone(context.Background(), "pve1", 9000, 100, "test")
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v", err)
	}
}

// --- Resize ---

func TestResize(t *testing.T) {
	var gotMethod, gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	if err := c.Resize(context.Background(), "pve1", 100, "scsi0", "+10G"); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.Contains(gotBody, "disk=scsi0") || !strings.Contains(gotBody, "size=%2B10G") {
		t.Errorf("body = %q", gotBody)
	}
}

// --- SetConfig ---

func TestSetConfig_HappyPath(t *testing.T) {
	var gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	err := c.SetConfig(context.Background(), "pve1", 100, map[string]string{
		"memory": "2048",
		"cores":  "2",
	})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if !strings.Contains(gotBody, "memory=2048") || !strings.Contains(gotBody, "cores=2") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestSetConfig_SSHKeysDoubleEncoded(t *testing.T) {
	rawKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample user@host"
	var gotForm url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(b))
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	err := c.SetConfig(context.Background(), "pve1", 100, map[string]string{"sshkeys": rawKey})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	// After form decoding, the server sees the once-url-encoded value.
	// Spaces must be %20 (not +) because PVE's sshkeys validator
	// rejects the form-urlencoded + variant as "invalid urlencoded
	// string". The @ in user@host must still be %40.
	want := strings.ReplaceAll(url.QueryEscape(rawKey), "+", "%20")
	if got := gotForm.Get("sshkeys"); got != want {
		t.Errorf("sshkeys = %q, want %q", got, want)
	}
	if strings.Contains(want, "+") {
		t.Errorf("expected value must not contain '+' for spaces: %q", want)
	}
}

// --- Start ---

func TestStart(t *testing.T) {
	var gotPath, gotMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:start:"}`))
	})
	upid, err := c.Start(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/nodes/pve1/qemu/100/status/start" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

// --- Shutdown ---

func TestShutdown_HappyPath(t *testing.T) {
	var gotPath, gotMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:shutdown:"}`))
	})
	upid, err := c.Shutdown(context.Background(), "pve1", 104)
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/nodes/pve1/qemu/104/status/shutdown" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

func TestShutdown_ServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Shutdown(context.Background(), "pve1", 104)
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v", err)
	}
}

// --- Stop ---

func TestStop_HappyPath(t *testing.T) {
	var gotPath, gotMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:stop:"}`))
	})
	upid, err := c.Stop(context.Background(), "pve1", 104)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/nodes/pve1/qemu/104/status/stop" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

func TestStop_ServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Stop(context.Background(), "pve1", 104)
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v", err)
	}
}

// --- GetStatus ---

func TestGetStatus_ParsesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/status_running.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	})
	st, err := c.GetStatus(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.Status != "running" {
		t.Errorf("Status = %q", st.Status)
	}
	if st.VMID != 100 || st.Name != "test-vm" || st.Uptime != 3600 || st.CPUs != 2 {
		t.Errorf("fields = %+v", st)
	}
	if st.Mem == 0 || st.MaxMem == 0 {
		t.Errorf("mem not populated: %+v", st)
	}
}

// --- GetConfig ---

func TestGetConfig_StringifiesMixedValues(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":{"cores":2,"memory":2048,"net0":"virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0","scsi0":"local-lvm:vm-104-disk-0,size=20G","template":1}}`))
	})
	cfg, err := c.GetConfig(context.Background(), "pve1", 104)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if gotPath != "/nodes/pve1/qemu/104/config" {
		t.Errorf("path = %q", gotPath)
	}
	wants := map[string]string{
		"cores":    "2",
		"memory":   "2048",
		"net0":     "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0",
		"scsi0":    "local-lvm:vm-104-disk-0,size=20G",
		"template": "1",
	}
	for k, want := range wants {
		if got := cfg[k]; got != want {
			t.Errorf("cfg[%q] = %q, want %q", k, got, want)
		}
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	var gotMethod, gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":"UPID:pve1:delete:"}`))
	})
	upid, err := c.Delete(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/nodes/pve1/qemu/100" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("upid = %q", upid)
	}
}

// --- AgentNetwork ---

func TestAgentNetwork_ParsesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/agent_network.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/agent/network-get-interfaces") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write(data)
	})
	ifaces, err := c.AgentNetwork(context.Background(), "pve1", 100)
	if err != nil {
		t.Fatalf("AgentNetwork: %v", err)
	}
	if len(ifaces) != 2 {
		t.Fatalf("ifaces len = %d", len(ifaces))
	}
	var eth0 *AgentIface
	for i := range ifaces {
		if ifaces[i].Name == "eth0" {
			eth0 = &ifaces[i]
		}
	}
	if eth0 == nil {
		t.Fatal("eth0 not found")
	}
	if len(eth0.IPAddresses) != 2 {
		t.Errorf("eth0 ips = %d", len(eth0.IPAddresses))
	}
	if eth0.HardwareAddr != "bc:24:11:ab:cd:ef" {
		t.Errorf("hw addr = %q", eth0.HardwareAddr)
	}
}

func TestAgentNetwork_AgentNotRunning(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.AgentNetwork(context.Background(), "pve1", 100)
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v", err)
	}
}

// --- WaitTask ---

func TestWaitTask_RunningThenOK(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		}
	})
	start := time.Now()
	err := c.WaitTask(context.Background(), "pve1", "UPID:x:", 5*time.Second)
	if err != nil {
		t.Fatalf("WaitTask: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
	if atomic.LoadInt32(&hits) < 3 {
		t.Errorf("only %d hits", hits)
	}
}

func TestWaitTask_FailureSurfacesExitStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"clone failed: destination VMID 200 already exists"}}`))
	})
	err := c.WaitTask(context.Background(), "pve1", "UPID:x:", 5*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "destination VMID 200 already exists") {
		t.Errorf("missing exit text: %v", err)
	}
}

func TestWaitTask_Timeout(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
	})
	err := c.WaitTask(context.Background(), "pve1", "UPID:x:", 600*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
}

func TestWaitTask_PreCancelledContext(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.WaitTask(ctx, "pve1", "UPID:x:", 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
}

// --- No-secret-leak coverage for new endpoints ---

func TestNoSecretInNewEndpoints(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		buf.WriteString(r.Method + " " + r.URL.Path + "\n")
		b, _ := io.ReadAll(r.Body)
		buf.Write(b)
		buf.WriteByte('\n')
		switch {
		case strings.HasSuffix(r.URL.Path, "/clone"):
			_, _ = w.Write([]byte(`{"data":"UPID:x:"}`))
		case strings.HasSuffix(r.URL.Path, "/start"):
			_, _ = w.Write([]byte(`{"data":"UPID:x:"}`))
		case strings.HasSuffix(r.URL.Path, "/resize"):
			_, _ = w.Write([]byte(`{"data":null}`))
		case strings.HasSuffix(r.URL.Path, "/config"):
			_, _ = w.Write([]byte(`{"data":null}`))
		case strings.HasSuffix(r.URL.Path, "/nextid"):
			_, _ = w.Write([]byte(`{"data":"100"}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	})
	ctx := context.Background()
	_, _ = c.NextID(ctx)
	_, _ = c.Clone(ctx, "pve1", 9000, 100, "t")
	_ = c.Resize(ctx, "pve1", 100, "scsi0", "+1G")
	_ = c.SetConfig(ctx, "pve1", 100, map[string]string{"memory": "1024"})
	_, _ = c.Start(ctx, "pve1", 100)
	if strings.Contains(buf.String(), "secret-value") {
		t.Errorf("secret leaked: %q", buf.String())
	}
}
