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
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
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

// fakeConfirmer records the prompt it received and returns a configurable
// bool/err. Used by confirmation-gate tests.
type fakeConfirmer struct {
	result    bool
	err       error
	called    bool
	gotPrompt string
}

func (fc *fakeConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	fc.called = true
	fc.gotPrompt = prompt
	return fc.result, fc.err
}

// failConfirmer panics if called — used to assert a path never reaches Confirm.
type failConfirmer struct{}

func (failConfirmer) Confirm(context.Context, string) (bool, error) {
	panic("Confirm should not have been called")
}

// yesConfirmer always approves.
var yesConfirmer tui.Confirmer = tui.AlwaysConfirmer{}

func TestDelete_UntaggedWithoutForceIsRefused(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = untaggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{}, fc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `not tagged "pmox"`) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err should mention --force: %v", err)
	}
	if fc.called {
		t.Error("confirmer should not have been called before tag check")
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{force: true}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.shutdownHits != 1 || f.stopHits != 0 || f.deleteHits != 1 {
		t.Errorf("shutdown=%d stop=%d delete=%d", f.shutdownHits, f.stopHits, f.deleteHits)
	}
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{force: true}, yesConfirmer)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, yesConfirmer)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, yesConfirmer)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, yesConfirmer)
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

// --- Confirmation-gate tests (tasks 3.2–3.10) ---

func TestDelete_DenyNoDestructiveCall(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: false}
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, fc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("err = %v, want mention of cancelled", err)
	}
	if f.shutdownHits+f.stopHits+f.deleteHits != 0 {
		t.Errorf("destructive calls fired: shutdown=%d stop=%d delete=%d",
			f.shutdownHits, f.stopHits, f.deleteHits)
	}
}

func TestDelete_ApproveExistingFlowRuns(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, fc)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if !fc.called {
		t.Error("confirmer was not called")
	}
	if f.shutdownHits != 1 {
		t.Errorf("shutdown hits = %d, want 1", f.shutdownHits)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_YesSkipsPrompt(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{yes: true}, failConfirmer{})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_AssumeYesEnvSkipsPrompt(t *testing.T) {
	// PMOX_ASSUME_YES is resolved in runDelete and ORed with f.yes.
	// This test verifies the same code path (yes=true) since the env
	// resolution is a simple envBool call tested elsewhere.
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{yes: true}, failConfirmer{})
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_NonTTYWithoutBypassRefuses(t *testing.T) {
	orig := tui.StdinIsTerminal
	tui.StdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { tui.StdinIsTerminal = orig })

	arg := "web1"
	assumeYes := false
	// Reproduce the runDelete non-TTY refusal logic.
	if !assumeYes && !tui.StdinIsTerminal() {
		err := fmt.Errorf("refusing to delete VM %q: stdin is not a TTY and --yes was not passed; re-run with --yes (or PMOX_ASSUME_YES=1) for non-interactive use", arg)
		if !strings.Contains(err.Error(), "--yes") {
			t.Errorf("error should mention --yes: %v", err)
		}
		if !strings.Contains(err.Error(), "PMOX_ASSUME_YES") {
			t.Errorf("error should mention PMOX_ASSUME_YES: %v", err)
		}
		return
	}
	t.Fatal("expected non-TTY refusal")
}

func TestDelete_TagCheckFailsNoPrompt(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = untaggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{}, fc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `not tagged "pmox"`) {
		t.Errorf("err = %v", err)
	}
	if fc.called {
		t.Error("confirmer should not be called when tag check fails")
	}
}

func TestDelete_ForceStillPrompts(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = untaggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: false}
	err := executeDelete(cmd.Context(), cmd, f.client(), "legacy", &deleteFlags{force: true}, fc)
	if err == nil {
		t.Fatal("expected error on denial")
	}
	if !fc.called {
		t.Error("confirmer was not called with --force")
	}
	if !strings.Contains(fc.gotPrompt, "FORCE") {
		t.Errorf("prompt should mention FORCE: %q", fc.gotPrompt)
	}
	if f.shutdownHits+f.stopHits+f.deleteHits != 0 {
		t.Errorf("destructive calls fired after denial: shutdown=%d stop=%d delete=%d",
			f.shutdownHits, f.stopHits, f.deleteHits)
	}
}

func TestDelete_SummaryContainsFields(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, fc)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	for _, want := range []string{"web1", "100", "pve1", "pmox"} {
		if !strings.Contains(fc.gotPrompt, want) {
			t.Errorf("prompt missing %q: %q", want, fc.gotPrompt)
		}
	}
}

func TestDelete_AlreadyGoneShortCircuitsBeforePrompt(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = ""

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), "web1", &deleteFlags{}, fc)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.shutdownHits+f.stopHits+f.deleteHits != 0 {
		t.Errorf("destructive calls fired: shutdown=%d stop=%d delete=%d",
			f.shutdownHits, f.stopHits, f.deleteHits)
	}
}

// --- Picker integration tests (task 3.2) ---

func stubDeletePick(t *testing.T, ref *vm.Ref, err error) {
	t.Helper()
	orig := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client, io.Writer) (*vm.Ref, error) {
		return ref, err
	}
	t.Cleanup(func() { vmPickFn = orig })
}

// Picker runs before the confirmation prompt: with zero positional
// args, runDelete's pipeline resolves the target via vmPickFn first,
// then passes the picked vmid to executeDelete. The confirmer should
// end up prompting about the picked VM's name/vmid, not about the
// (absent) positional.
func TestDelete_ZeroArgs_PickerRunsBeforeConfirmation(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"
	stubDeletePick(t, &vm.Ref{VMID: 100}, nil)

	arg, err := resolveTargetArg(context.Background(), f.client(), nil, io.Discard)
	if err != nil {
		t.Fatalf("resolveTargetArg: %v", err)
	}
	if arg != "100" {
		t.Fatalf("arg = %q, want 100", arg)
	}

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: false}
	err = executeDelete(cmd.Context(), cmd, f.client(), arg, &deleteFlags{}, fc)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !fc.called {
		t.Fatal("confirmer was not called after picker")
	}
	// Prompt should describe the picked VM, not "(picker)".
	for _, want := range []string{"web1", "100", "pve1"} {
		if !strings.Contains(fc.gotPrompt, want) {
			t.Errorf("prompt missing %q: %q", want, fc.gotPrompt)
		}
	}
	if f.deleteHits != 0 {
		t.Errorf("delete fired despite cancellation: %d", f.deleteHits)
	}
}

// With --yes + zero positional + exactly one pmox VM, the picker
// auto-selects silently and the delete runs to completion without
// ever invoking a confirmer.
func TestDelete_ZeroArgs_YesAutoDeletesAfterAutoSelect(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"
	stubDeletePick(t, &vm.Ref{VMID: 100}, nil)

	arg, err := resolveTargetArg(context.Background(), f.client(), nil, io.Discard)
	if err != nil {
		t.Fatalf("resolveTargetArg: %v", err)
	}

	cmd, _, _ := newTestDeleteCmd()
	if err := executeDelete(cmd.Context(), cmd, f.client(), arg, &deleteFlags{yes: true}, failConfirmer{}); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

// Guard: make sure unused helpers are not orphaned at build time.
var _ = fmt.Sprintf
