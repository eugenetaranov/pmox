package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// fakePVE is a minimal httptest-backed PVE server for the delete
// command tests. Each handler increments an atomic counter so tests
// can assert which endpoints were hit (and which were NOT).
type fakePVE struct {
	srv *httptest.Server

	// cluster resources payload returned on /cluster/resources
	clusterBody string
	// vmStatus controls what GetStatus returns; empty string triggers 404.
	vmStatus string

	clusterHits  int32
	statusHits   int32
	shutdownHits int32
	stopHits     int32
	deleteHits   int32
	taskHits     int32
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	f := &fakePVE{vmStatus: "running"}
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

		case strings.HasSuffix(p, "/status/shutdown") && r.Method == "POST":
			atomic.AddInt32(&f.shutdownHits, 1)
			_, _ = io.WriteString(w, `{"data":"UPID:pve1:shutdown:"}`)

		case strings.HasSuffix(p, "/status/stop") && r.Method == "POST":
			atomic.AddInt32(&f.stopHits, 1)
			_, _ = io.WriteString(w, `{"data":"UPID:pve1:stop:"}`)

		case strings.HasPrefix(p, "/nodes/") && strings.Contains(p, "/tasks/") && strings.HasSuffix(p, "/status"):
			atomic.AddInt32(&f.taskHits, 1)
			_, _ = io.WriteString(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)

		case r.Method == "DELETE" && strings.HasPrefix(p, "/nodes/") && strings.Contains(p, "/qemu/"):
			atomic.AddInt32(&f.deleteHits, 1)
			_, _ = io.WriteString(w, `{"data":"UPID:pve1:delete:"}`)

		default:
			t.Errorf("unhandled request: %s %s", r.Method, p)
			http.Error(w, "unhandled", 500)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePVE) client() *pveclient.Client {
	return &pveclient.Client{
		BaseURL:    f.srv.URL,
		TokenID:    "t",
		Secret:     "s",
		HTTPClient: f.srv.Client(),
	}
}

// newTestDeleteCmd returns a cobra.Command plumbed with a buffer
// stdout/stderr and a context — but with no real RunE. Tests drive
// executeDelete directly and inspect the buffers.
func newTestDeleteCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "delete"}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

const taggedRunningVM = `{"data":[
  {"vmid":100,"name":"web1","node":"pve1","status":"running","tags":"pmox"}
]}`

const untaggedRunningVM = `{"data":[
  {"vmid":200,"name":"legacy","node":"pve1","status":"running","tags":""}
]}`

const taggedStoppedVM = `{"data":[
  {"vmid":100,"name":"web1","node":"pve1","status":"stopped","tags":"pmox"}
]}`

const dupeNameVMs = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":107,"name":"web1","node":"pve2","status":"running","tags":"pmox"}
]}`

func TestDelete_UntaggedWithoutForceIsRefused(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = untaggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `not tagged "pmox"`) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err should mention --force: %v", err)
	}
	if f.statusHits != 0 || f.shutdownHits != 0 || f.stopHits != 0 || f.deleteHits != 0 {
		t.Errorf("destructive calls fired: status=%d shutdown=%d stop=%d delete=%d",
			f.statusHits, f.shutdownHits, f.stopHits, f.deleteHits)
	}
}

func TestDelete_UntaggedWithForceProceeds(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = untaggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{force: true})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	// --force uses hard stop, not shutdown.
	if f.stopHits != 1 {
		t.Errorf("stop hits = %d, want 1", f.stopHits)
	}
	if f.shutdownHits != 0 {
		t.Errorf("shutdown hits = %d, want 0 (force uses stop)", f.shutdownHits)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_RunningShutdownThenDestroy(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, out, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.shutdownHits != 1 || f.stopHits != 0 || f.deleteHits != 1 {
		t.Errorf("shutdown=%d stop=%d delete=%d", f.shutdownHits, f.stopHits, f.deleteHits)
	}
	// Two WaitTask polls: one for shutdown, one for delete.
	if f.taskHits < 2 {
		t.Errorf("task hits = %d, want >= 2", f.taskHits)
	}
	if !strings.Contains(out.String(), `Deleted VM "web1"`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestDelete_RunningForceUsesHardStop(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{force: true})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.stopHits != 1 {
		t.Errorf("stop hits = %d, want 1", f.stopHits)
	}
	if f.shutdownHits != 0 {
		t.Errorf("shutdown hits = %d, want 0", f.shutdownHits)
	}
}

func TestDelete_StoppedVMSkipsShutdown(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedStoppedVM
	f.vmStatus = "stopped"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.shutdownHits != 0 || f.stopHits != 0 {
		t.Errorf("shutdown/stop fired on stopped VM: shutdown=%d stop=%d", f.shutdownHits, f.stopHits)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_AlreadyGoneIsSuccess(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "" // triggers 404 on /status/current

	cmd, _, errb := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if !strings.Contains(errb.String(), "already gone") {
		t.Errorf("stderr = %q", errb.String())
	}
	if f.shutdownHits != 0 || f.stopHits != 0 || f.deleteHits != 0 {
		t.Errorf("destructive calls fired after already-gone: shutdown=%d stop=%d delete=%d",
			f.shutdownHits, f.stopHits, f.deleteHits)
	}
}

func TestDelete_AmbiguousNameFailsEarly(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = dupeNameVMs

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "multiple VMs") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(msg, "104") || !strings.Contains(msg, "107") {
		t.Errorf("err should list both vmids: %v", err)
	}
	if f.statusHits != 0 || f.shutdownHits != 0 || f.stopHits != 0 || f.deleteHits != 0 {
		t.Errorf("destructive calls fired on ambiguous name")
	}
}

// Guard: make sure unused helpers are not orphaned at build time.
var _ = fmt.Sprintf
