package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/pvetest"
)

// cloneSourceVM is the source that clone_test resolves against. VMID
// 500 lets us assert the launch state-machine targets the resolved
// source ID, not the built-in template.
const cloneSourceVM = `{"data":[
  {"vmid":500,"name":"web1","node":"pve1","status":"running","tags":"pmox"}
]}`

func TestClone_DrivesLaunchStateMachineFromSourceVMID(t *testing.T) {
	f := pvetest.New(t)
	// Resolve: one source VM.
	f.Handle("GET", "/cluster/resources", pvetest.JSON(cloneSourceVM))
	// Launch state machine.
	f.Handle("GET", "/cluster/nextid", pvetest.JSON(`{"data":"600"}`))

	var cloneSourcePath string
	f.Handle("POST", "/clone", func(w http.ResponseWriter, r *http.Request, _ string) {
		cloneSourcePath = r.URL.Path
		fmt.Fprint(w, `{"data":"UPID:pve1:clone:"}`)
	})
	f.Handle("GET", "/tasks/", pvetest.TaskOK)
	f.Handle("POST", "/config", pvetest.JSON(`{"data":null}`))
	f.Handle("PUT", "/resize", pvetest.JSON(`{"data":null}`))
	f.Handle("POST", "/status/start", pvetest.JSON(`{"data":"UPID:pve1:start:"}`))

	var agentHits int32
	f.Handle("GET", "/agent/network-get-interfaces", func(w http.ResponseWriter, _ *http.Request, _ string) {
		atomic.AddInt32(&agentHits, 1)
		fmt.Fprint(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"10.0.0.7"}]}]}}`)
	})

	cmd, out, _ := newTestInfoCmd()
	partial := launch.Options{
		User:      "pmox",
		SSHPubKey: "ssh-ed25519 AAAA test@host",
		CPU:       2,
		MemMB:     2048,
		DiskSize:  "20G",
		Wait:      5 * time.Second,
		NoWaitSSH: true,
	}
	if err := executeClone(cmd.Context(), cmd, f.Client(), "web1", "web1-copy", partial); err != nil {
		t.Fatalf("executeClone: %v", err)
	}

	// Clone must target /qemu/500/clone — the resolved source VMID.
	if !strings.Contains(cloneSourcePath, "/qemu/500/clone") {
		t.Errorf("clone path = %q, want /qemu/500/clone", cloneSourcePath)
	}
	// NextID is called for the new VMID (600).
	if f.Count("GET", "/cluster/nextid") != 1 {
		t.Errorf("nextid hits = %d", f.Count("GET", "/cluster/nextid"))
	}
	// New VMID (600) gets tagged + resized + config + start.
	if f.Count("POST", "/qemu/600/config") < 2 {
		t.Errorf("config hits on 600 = %d, want >=2 (tag + full kv)", f.Count("POST", "/qemu/600/config"))
	}
	if f.Count("PUT", "/qemu/600/resize") != 1 {
		t.Errorf("resize hits = %d", f.Count("PUT", "/qemu/600/resize"))
	}
	if f.Count("POST", "/qemu/600/status/start") != 1 {
		t.Errorf("start hits = %d", f.Count("POST", "/qemu/600/status/start"))
	}
	if atomic.LoadInt32(&agentHits) == 0 {
		t.Errorf("expected agent poll")
	}
	if !strings.Contains(out.String(), "cloned web1 -> web1-copy") {
		t.Errorf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "vmid=600") {
		t.Errorf("stdout missing vmid=600: %q", out.String())
	}
	if !strings.Contains(out.String(), "ip=10.0.0.7") {
		t.Errorf("stdout missing ip: %q", out.String())
	}
}
