package main

import (
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pvetest"
)

func newStopFake(t *testing.T) *pvetest.Server {
	t.Helper()
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(oneVMResources))
	f.Handle("POST", "/status/shutdown", pvetest.JSON(`{"data":"UPID:pve1:shutdown:"}`))
	f.Handle("POST", "/status/stop", pvetest.JSON(`{"data":"UPID:pve1:stop:"}`))
	f.Handle("GET", "/tasks/", pvetest.TaskOK)
	return f
}

func TestStop_DefaultRoutesToShutdown(t *testing.T) {
	f := newStopFake(t)
	cmd, out, _ := newTestInfoCmd()
	if err := executeStop(cmd.Context(), cmd, f.Client(), "web1", &stopFlags{}); err != nil {
		t.Fatalf("executeStop: %v", err)
	}
	if f.Count("POST", "/status/shutdown") != 1 {
		t.Errorf("shutdown hits = %d", f.Count("POST", "/status/shutdown"))
	}
	if f.Count("POST", "/status/stop") != 0 {
		t.Errorf("stop hits = %d, want 0", f.Count("POST", "/status/stop"))
	}
	if !strings.Contains(out.String(), "shutdown web1") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestStop_ForceRoutesToStop(t *testing.T) {
	f := newStopFake(t)
	cmd, out, _ := newTestInfoCmd()
	if err := executeStop(cmd.Context(), cmd, f.Client(), "web1", &stopFlags{force: true}); err != nil {
		t.Fatalf("executeStop: %v", err)
	}
	if f.Count("POST", "/status/stop") != 1 {
		t.Errorf("stop hits = %d", f.Count("POST", "/status/stop"))
	}
	if f.Count("POST", "/status/shutdown") != 0 {
		t.Errorf("shutdown hits = %d, want 0", f.Count("POST", "/status/shutdown"))
	}
	if !strings.Contains(out.String(), "stop web1") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestStop_NoWaitSkipsTaskPoll(t *testing.T) {
	f := newStopFake(t)
	cmd, _, _ := newTestInfoCmd()
	if err := executeStop(cmd.Context(), cmd, f.Client(), "web1", &stopFlags{noWait: true}); err != nil {
		t.Fatalf("executeStop: %v", err)
	}
	if f.Count("GET", "/tasks/") != 0 {
		t.Errorf("--no-wait must skip task polls, got %d", f.Count("GET", "/tasks/"))
	}
}
