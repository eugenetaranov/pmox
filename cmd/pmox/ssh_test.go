package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// fakeSSHPVE extends the minimal PVE fake with agent network support.
type fakeSSHPVE struct {
	srv *httptest.Server

	clusterBody string
	vmStatus    string // "running", "stopped", or "" (404)
	agentBody   string // JSON for agent/network-get-interfaces
	agentErr    bool   // if true, agent endpoint returns 500

	clusterHits int32
	statusHits  int32
	startHits   int32
	taskHits    int32
	agentHits   int32
}

func newFakeSSHPVE(t *testing.T) *fakeSSHPVE {
	t.Helper()
	f := &fakeSSHPVE{vmStatus: "running"}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/cluster/resources"):
			atomic.AddInt32(&f.clusterHits, 1)
			if f.clusterBody == "" {
				http.Error(w, "no fixture", 500)
				return
			}
			_, _ = io.WriteString(w, f.clusterBody)

		case strings.HasSuffix(p, "/status/current"):
			atomic.AddInt32(&f.statusHits, 1)
			if f.vmStatus == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"status": f.vmStatus,
					"vmid":   100,
					"name":   "web1",
				},
			})

		case strings.HasSuffix(p, "/status/start") && r.Method == "POST":
			atomic.AddInt32(&f.startHits, 1)
			_, _ = io.WriteString(w, `{"data":"UPID:pve1:start:"}`)

		case strings.HasPrefix(p, "/nodes/") && strings.Contains(p, "/tasks/") && strings.HasSuffix(p, "/status"):
			atomic.AddInt32(&f.taskHits, 1)
			_, _ = io.WriteString(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)

		case strings.Contains(p, "/agent/network-get-interfaces"):
			atomic.AddInt32(&f.agentHits, 1)
			if f.agentErr {
				http.Error(w, "agent not running", 500)
				return
			}
			if f.agentBody == "" {
				_, _ = io.WriteString(w, `{"data":{"result":[]}}`)
				return
			}
			_, _ = io.WriteString(w, f.agentBody)

		default:
			t.Errorf("unhandled request: %s %s", r.Method, p)
			http.Error(w, "unhandled", 500)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSSHPVE) client() *pveclient.Client {
	return &pveclient.Client{
		BaseURL:    f.srv.URL,
		TokenID:    "t",
		Secret:     "s",
		HTTPClient: f.srv.Client(),
	}
}

func newTestSSHCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "ssh"}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

const sshTaggedRunningVM = `{"data":[
  {"vmid":100,"name":"web1","node":"pve1","status":"running","tags":"pmox"}
]}`

const sshUntaggedRunningVM = `{"data":[
  {"vmid":200,"name":"legacy","node":"pve1","status":"running","tags":""}
]}`

const agentWithIP = `{"data":{"result":[
  {"name":"eth0","hardware-address":"00:11:22:33:44:55","ip-addresses":[
    {"ip-address-type":"ipv4","ip-address":"192.168.1.10","prefix":24}
  ]}
]}}`

const agentNoIPv4 = `{"data":{"result":[
  {"name":"eth0","hardware-address":"00:11:22:33:44:55","ip-addresses":[
    {"ip-address-type":"ipv6","ip-address":"fe80::1","prefix":64}
  ]}
]}}`

// --- resolveSSHTarget tests ---

func TestSSH_ResolveTaggedRunningVM(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshTaggedRunningVM
	f.agentBody = agentWithIP

	cmd, _, _ := newTestSSHCmd()
	target, err := resolveSSHTarget(cmd.Context(), cmd, f.client(), "web1",
		&sshFlags{user: "pmox"}, "")
	if err != nil {
		t.Fatalf("resolveSSHTarget: %v", err)
	}
	if target.IP != "192.168.1.10" {
		t.Errorf("ip = %q, want 192.168.1.10", target.IP)
	}
	if target.User != "pmox" {
		t.Errorf("user = %q, want pmox", target.User)
	}
}

func TestSSH_ResolveUntaggedWithoutForce(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshUntaggedRunningVM

	cmd, _, _ := newTestSSHCmd()
	_, err := resolveSSHTarget(cmd.Context(), cmd, f.client(), "legacy",
		&sshFlags{user: "pmox"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `not tagged "pmox"`) {
		t.Errorf("err = %v", err)
	}
	if f.agentHits != 0 {
		t.Errorf("agent was called despite tag failure")
	}
}

func TestSSH_ResolveUntaggedWithForce(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshUntaggedRunningVM
	f.agentBody = agentWithIP

	cmd, _, _ := newTestSSHCmd()
	target, err := resolveSSHTarget(cmd.Context(), cmd, f.client(), "legacy",
		&sshFlags{user: "pmox", force: true}, "")
	if err != nil {
		t.Fatalf("resolveSSHTarget: %v", err)
	}
	if target.IP != "192.168.1.10" {
		t.Errorf("ip = %q, want 192.168.1.10", target.IP)
	}
}

// --- getOrStartVM tests ---

func TestSSH_GetOrStartVM_Running(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshTaggedRunningVM
	f.agentBody = agentWithIP

	cmd, _, _ := newTestSSHCmd()
	ref := &vm.Ref{VMID: 100, Node: "pve1", Name: "web1"}
	ip, err := getOrStartVM(cmd.Context(), cmd, f.client(), ref)
	if err != nil {
		t.Fatalf("getOrStartVM: %v", err)
	}
	if ip != "192.168.1.10" {
		t.Errorf("ip = %q", ip)
	}
	if f.startHits != 0 {
		t.Errorf("Start was called on running VM")
	}
	if f.agentHits != 1 {
		t.Errorf("agent hits = %d, want 1", f.agentHits)
	}
}

func TestSSH_GetOrStartVM_AgentNoIP(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshTaggedRunningVM
	f.agentBody = agentNoIPv4

	cmd, _, _ := newTestSSHCmd()
	ref := &vm.Ref{VMID: 100, Node: "pve1", Name: "web1"}
	_, err := getOrStartVM(cmd.Context(), cmd, f.client(), ref)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no usable IPv4") {
		t.Errorf("err = %v", err)
	}
}

func TestSSH_GetOrStartVM_AgentNotResponding(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshTaggedRunningVM
	f.agentErr = true

	cmd, _, _ := newTestSSHCmd()
	ref := &vm.Ref{VMID: 100, Node: "pve1", Name: "web1"}
	_, err := getOrStartVM(cmd.Context(), cmd, f.client(), ref)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "qemu-guest-agent") {
		t.Errorf("err = %v, want mention of qemu-guest-agent", err)
	}
}

func TestSSH_GetOrStartVM_NotFound(t *testing.T) {
	f := newFakeSSHPVE(t)
	f.clusterBody = sshTaggedRunningVM
	f.vmStatus = ""

	cmd, _, _ := newTestSSHCmd()
	ref := &vm.Ref{VMID: 100, Node: "pve1", Name: "web1"}
	_, err := getOrStartVM(cmd.Context(), cmd, f.client(), ref)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

// --- buildSSHArgs tests ---

func TestSSH_BuildArgsWithIdentity(t *testing.T) {
	args := buildSSHArgs("/usr/bin/ssh", &sshTarget{
		IP: "10.0.0.1", User: "pmox", Key: "/home/user/.ssh/id_ed25519",
	}, nil)
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-i /home/user/.ssh/id_ed25519") {
		t.Errorf("args missing -i: %v", args)
	}
	if !strings.Contains(got, "pmox@10.0.0.1") {
		t.Errorf("args missing user@ip: %v", args)
	}
	if !strings.Contains(got, "StrictHostKeyChecking=no") {
		t.Errorf("args missing StrictHostKeyChecking: %v", args)
	}
}

func TestSSH_BuildArgsWithoutIdentity(t *testing.T) {
	args := buildSSHArgs("/usr/bin/ssh", &sshTarget{
		IP: "10.0.0.1", User: "pmox", Key: "",
	}, nil)
	for _, a := range args {
		if a == "-i" {
			t.Error("args should not contain -i when key is empty")
		}
	}
}

func TestSSH_BuildArgsWithExtraArgs(t *testing.T) {
	args := buildSSHArgs("/usr/bin/ssh", &sshTarget{
		IP: "10.0.0.1", User: "pmox", Key: "",
	}, []string{"uname", "-a"})
	last2 := args[len(args)-2:]
	if last2[0] != "uname" || last2[1] != "-a" {
		t.Errorf("extra args not appended: %v", args)
	}
}

// --- derivePrivateKeyPath tests ---

func TestSSH_DerivePrivateKeyPath(t *testing.T) {
	got := derivePrivateKeyPath("~/.ssh/id_ed25519.pub")
	if got != "~/.ssh/id_ed25519" {
		t.Errorf("got %q, want ~/.ssh/id_ed25519", got)
	}
}

func TestSSH_ResolveIdentityKey_Explicit(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "mykey")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveIdentityKey(keyPath, "")
	if err != nil {
		t.Fatalf("resolveIdentityKey: %v", err)
	}
	if got != keyPath {
		t.Errorf("got %q, want %q", got, keyPath)
	}
}

func TestSSH_ResolveIdentityKey_DerivedNotFound(t *testing.T) {
	_, err := resolveIdentityKey("", "/nonexistent/id_ed25519.pub")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--identity") {
		t.Errorf("err = %v, want mention of --identity", err)
	}
}

func TestSSH_ResolveIdentityKey_NothingConfigured(t *testing.T) {
	got, err := resolveIdentityKey("", "")
	if err != nil {
		t.Fatalf("resolveIdentityKey: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// Guard: make sure unused helpers are not orphaned at build time.
var _ = fmt.Sprintf
