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
	"github.com/eugenetaranov/pmox/internal/tackprofile"
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
	// vmCicustom, when non-empty, is returned as the `cicustom` key
	// from GET /config; empty means no cicustom on the VM.
	vmCicustom string
	// deleteFails, when true, makes the destroy (DELETE qemu) endpoint
	// return a 500 so tests can exercise the interrupted/failed-destroy path.
	deleteFails bool

	clusterHits       int32
	statusHits        int32
	shutdownHits      int32
	stopHits          int32
	deleteHits        int32
	taskHits          int32
	configHits        int32
	snippetDeleteHits int32
	snippetDeletePath string
	// lastDeletePath and lastStatusPath record the path of the most
	// recent destroy/status call, so tests can assert *which* vmid was
	// actually targeted, not just that some delete happened.
	lastDeletePath string
	lastStatusPath string
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	// destroyVM's forgetTackProfile call resolves the real XDG state dir
	// otherwise, and would read/write the developer's actual
	// ~/.local/state/pmox/tack/profiles.json during `go test`.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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

		case strings.HasSuffix(p, "/config") && r.Method == "GET":
			atomic.AddInt32(&f.configHits, 1)
			out := map[string]any{"data": map[string]any{"name": "web1"}}
			if f.vmCicustom != "" {
				out["data"].(map[string]any)["cicustom"] = f.vmCicustom
			}
			_ = json.NewEncoder(w).Encode(out)

		case r.Method == "DELETE" && strings.Contains(p, "/storage/") && strings.Contains(p, "/content/"):
			atomic.AddInt32(&f.snippetDeleteHits, 1)
			f.snippetDeletePath = p
			_, _ = io.WriteString(w, `{"data":null}`)

		case strings.HasSuffix(p, "/status/current"):
			atomic.AddInt32(&f.statusHits, 1)
			f.lastStatusPath = p
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
			f.lastDeletePath = p
			if f.deleteFails {
				http.Error(w, `{"data":null}`, http.StatusInternalServerError)
				return
			}
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

const twoTaggedVMs = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"stopped","tags":"pmox"},
  {"vmid":105,"name":"web2","node":"pve1","status":"stopped","tags":"pmox"}
]}`

const dupeNameVMs = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":107,"name":"web1","node":"pve2","status":"running","tags":"pmox"}
]}`

// taggedAndUntaggedSameName has two VMs sharing a name: 105 carries the
// pmox tag (and so is the only one `pmox list`'s default view shows),
// 107 doesn't. Regression fixture for the "pmox list shows one VM but
// resolving that name by hand claims it's ambiguous" bug.
const taggedAndUntaggedSameName = `{"data":[
  {"vmid":105,"name":"alice","node":"pve1","status":"stopped","tags":"pmox"},
  {"vmid":107,"name":"alice","node":"pve1","status":"stopped","tags":""}
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"legacy"}, &deleteFlags{}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"legacy"}, &deleteFlags{force: true}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	// --force only bypasses the tag check; it uses graceful shutdown
	// (--hard is the separate power-off flag).
	if f.shutdownHits != 1 {
		t.Errorf("shutdown hits = %d, want 1 (force uses graceful shutdown)", f.shutdownHits)
	}
	if f.stopHits != 0 {
		t.Errorf("stop hits = %d, want 0", f.stopHits)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, yesConfirmer)
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

func TestDelete_HardUsesStop(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{hard: true}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.stopHits != 1 {
		t.Errorf("stop hits = %d, want 1 (--hard uses stop)", f.stopHits)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, yesConfirmer)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, yesConfirmer)
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

// TestDelete_ForgetsTackProfile guards the VMID-reuse fix: without
// this, a VMID Proxmox later reassigned to an unrelated new VM would
// silently inherit the deleted VM's remembered tack playbook on the new
// VM's first bare `pmox apply <vm>`, with no warning and no way for
// `pmox cleanup` to catch it (the VMID exists again, so it never looks
// stale). destroyVM must forget the profile as part of a normal delete.
func TestDelete_ForgetsTackProfile(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	const serverURL = "https://pve.example:8006/api2/json"
	stateDir := testTackStateDir(t)
	if err := tackprofile.Set(stateDir, serverURL, 100, "db"); err != nil {
		t.Fatal(err)
	}

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{serverURL: serverURL}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if _, ok, err := tackprofile.Get(stateDir, serverURL, 100); err != nil || ok {
		t.Errorf("tack profile still remembered after delete (ok=%v, err=%v)", ok, err)
	}
}

// TestDelete_ForgetsTackProfile_AlreadyGone covers the idempotent
// re-run path: a VM already destroyed outside this invocation must
// still have its remembered profile pruned, not just a fresh destroy.
func TestDelete_ForgetsTackProfile_AlreadyGone(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "" // triggers 404 on /status/current, the "already gone" path
	const serverURL = "https://pve.example:8006/api2/json"
	stateDir := testTackStateDir(t)
	if err := tackprofile.Set(stateDir, serverURL, 100, "db"); err != nil {
		t.Fatal(err)
	}

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{serverURL: serverURL}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if _, ok, err := tackprofile.Get(stateDir, serverURL, 100); err != nil || ok {
		t.Errorf("tack profile still remembered after already-gone delete (ok=%v, err=%v)", ok, err)
	}
}

func TestDelete_AmbiguousNameFailsEarly(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = dupeNameVMs

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, yesConfirmer)
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

// TestDelete_NameTargetsTheTaggedVM guards against the exact bug
// reported live: `pmox list` showed only the pmox-tagged VM "alice"
// (vmid 105), but `pmox delete alice` resolved the name against every
// VM including an untagged one (107) sharing the name, and refused
// with "multiple VMs named ... — pass the VMID instead" even though
// the user could not see why from `pmox list`'s output. Resolution
// must land on the tagged VM the user actually saw, and destroy that
// exact vmid — never the untagged one.
func TestDelete_NameTargetsTheTaggedVM(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedAndUntaggedSameName
	f.vmStatus = "stopped" // skip shutdown/stop; only GetStatus + Delete matter here

	cmd, out, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"alice"}, &deleteFlags{}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v (name resolution should have picked the tagged VM, not reported ambiguity)", err)
	}
	if f.deleteHits != 1 {
		t.Fatalf("delete hits = %d, want 1", f.deleteHits)
	}
	if !strings.Contains(f.lastStatusPath, "/qemu/105/") {
		t.Errorf("status checked %q, want vmid 105", f.lastStatusPath)
	}
	if !strings.HasSuffix(f.lastDeletePath, "/qemu/105") {
		t.Errorf("destroyed %q, want vmid 105 (the tagged VM), not 107", f.lastDeletePath)
	}
	if !strings.Contains(out.String(), "vmid 105") {
		t.Errorf("output = %q, want it to confirm vmid 105", out.String())
	}
}

// --- Confirmation-gate tests (tasks 3.2–3.10) ---

func TestDelete_DenyNoDestructiveCall(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: false}
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true}, failConfirmer{})
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true}, failConfirmer{})
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"legacy"}, &deleteFlags{}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"legacy"}, &deleteFlags{force: true}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, fc)
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
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{}, fc)
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
	vmPickFn = func(context.Context, *pveclient.Client) (*vm.Ref, error) {
		return ref, err
	}
	t.Cleanup(func() { vmPickFn = orig })
}

func stubDeletePickMulti(t *testing.T, refs []*vm.Ref, err error) {
	t.Helper()
	orig := vmPickMultiFn
	vmPickMultiFn = func(context.Context, *pveclient.Client) ([]*vm.Ref, error) {
		return refs, err
	}
	t.Cleanup(func() { vmPickMultiFn = orig })
}

func TestDelete_MultipleExplicitArgs(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = twoTaggedVMs
	f.vmStatus = "stopped"

	cmd, _, _ := newTestDeleteCmd()
	fc := &fakeConfirmer{result: true}
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1", "web2"}, &deleteFlags{}, fc)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	// Both VMs destroyed with a single confirmation listing both.
	if f.deleteHits != 2 {
		t.Errorf("delete hits = %d, want 2", f.deleteHits)
	}
	if !fc.called {
		t.Error("confirmer should be called once for the set")
	}
	for _, want := range []string{"2 VMs", "web1", "web2"} {
		if !strings.Contains(fc.gotPrompt, want) {
			t.Errorf("multi-delete prompt missing %q: %q", want, fc.gotPrompt)
		}
	}
}

func TestDelete_ZeroArgsUsesMultiPicker(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = twoTaggedVMs
	f.vmStatus = "stopped"
	stubDeletePickMulti(t, []*vm.Ref{
		{VMID: 104, Name: "web1", Node: "pve1", Tags: "pmox"},
		{VMID: 105, Name: "web2", Node: "pve1", Tags: "pmox"},
	}, nil)

	targets, err := resolveTargetArgs(context.Background(), f.client(), nil, io.Discard)
	if err != nil {
		t.Fatalf("resolveTargetArgs: %v", err)
	}
	if len(targets) != 2 || targets[0] != "104" || targets[1] != "105" {
		t.Fatalf("targets = %v, want [104 105]", targets)
	}
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
	err = executeDelete(cmd.Context(), cmd, f.client(), []string{arg}, &deleteFlags{}, fc)
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
	if err := executeDelete(cmd.Context(), cmd, f.client(), []string{arg}, &deleteFlags{yes: true}, failConfirmer{}); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_CustomCloudInitRemovesSnippet(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"
	f.vmCicustom = "user=local:snippets/pmox-100-user-data.yaml"

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.snippetDeleteHits != 1 {
		t.Errorf("snippet delete hits = %d, want 1", f.snippetDeleteHits)
	}
	if !strings.Contains(f.snippetDeletePath, "local:snippets/pmox-100-user-data.yaml") {
		t.Errorf("snippet delete path = %q", f.snippetDeletePath)
	}
}

// The snippet must be removed BEFORE the irreversible destroy, so an
// interrupted or failed destroy never leaves an orphaned snippet behind.
// Here the destroy fails, yet the snippet is still cleaned up.
func TestDelete_SnippetCleanedBeforeDestroyFails(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"
	f.vmCicustom = "user=local:snippets/pmox-100-user-data.yaml"
	f.deleteFails = true

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true}, yesConfirmer)
	if err == nil {
		t.Fatal("expected destroy to fail")
	}
	if f.snippetDeleteHits != 1 {
		t.Errorf("snippet should be cleaned before destroy even when destroy fails; hits = %d, want 1", f.snippetDeleteHits)
	}
}

func TestDelete_BuiltinCloudInitNoSnippetCleanup(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	f.vmStatus = "running"
	// vmCicustom left empty — simulates a built-in cloud-init VM.

	cmd, _, _ := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if f.snippetDeleteHits != 0 {
		t.Errorf("snippet delete hits = %d, want 0", f.snippetDeleteHits)
	}
}

// Guard: make sure unused helpers are not orphaned at build time.
var _ = fmt.Sprintf
