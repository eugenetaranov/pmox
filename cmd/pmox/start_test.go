package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/pvetest"
)

func TestStart_HappyPathWaitsForIP(t *testing.T) {
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(oneVMResources))
	f.Handle("POST", "/status/start", pvetest.JSON(`{"data":"UPID:pve1:start:"}`))
	f.Handle("GET", "/tasks/", pvetest.TaskOK)
	f.Handle("GET", "/agent/network-get-interfaces", pvetest.JSON(web1AgentNet))

	cmd, out, _ := newTestInfoCmd()
	err := executeStart(cmd.Context(), cmd, f.Client(), "web1", &startFlags{wait: 5 * time.Second})
	if err != nil {
		t.Fatalf("executeStart: %v", err)
	}
	if !strings.Contains(out.String(), "started web1") {
		t.Errorf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "192.168.1.43") {
		t.Errorf("stdout missing IP: %q", out.String())
	}
	if f.Count("POST", "/status/start") != 1 {
		t.Errorf("start calls = %d", f.Count("POST", "/status/start"))
	}
	if f.Count("GET", "/agent/network-get-interfaces") == 0 {
		t.Errorf("expected agent poll, got 0")
	}
	if f.Count("GET", "/tasks/") == 0 {
		t.Errorf("expected WaitTask poll, got 0")
	}
}

func TestStart_NoWaitSkipsAgent(t *testing.T) {
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(oneVMResources))
	f.Handle("POST", "/status/start", pvetest.JSON(`{"data":"UPID:pve1:start:"}`))
	f.Handle("GET", "/tasks/", pvetest.TaskOK)
	var agentCalls int32
	f.Handle("GET", "/agent/network-get-interfaces", func(_ http.ResponseWriter, _ *http.Request, _ string) {
		atomic.AddInt32(&agentCalls, 1)
	})

	cmd, out, _ := newTestInfoCmd()
	err := executeStart(cmd.Context(), cmd, f.Client(), "web1", &startFlags{noWait: true})
	if err != nil {
		t.Fatalf("executeStart: %v", err)
	}
	if !strings.Contains(out.String(), "started web1") {
		t.Errorf("stdout = %q", out.String())
	}
	if atomic.LoadInt32(&agentCalls) != 0 {
		t.Errorf("--no-wait must skip agent calls, got %d", agentCalls)
	}
}
