package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/huh"
)

const oneTaggedFixture = `{"data":[
  {"vmid":100,"name":"smoke","node":"p0","status":"running","tags":"pmox"}
]}`

const threeTaggedFixture = `{"data":[
  {"vmid":200,"name":"beta","node":"p0","status":"running","tags":"pmox"},
  {"vmid":100,"name":"alpha","node":"p0","status":"stopped","tags":"pmox"},
  {"vmid":300,"name":"legacy","node":"p1","status":"running","tags":""}
]}`

const noTaggedFixture = `{"data":[
  {"vmid":300,"name":"legacy","node":"p1","status":"running","tags":""}
]}`

// withPickerStubs temporarily overrides the TTY + picker injection
// points and restores them when the returned func is invoked.
func withPickerStubs(t *testing.T, stdinTTY, stderrTTY bool, pick func(string, []huh.Option[string], string) string) func() {
	t.Helper()
	origStdin, origStderr, origSelect := isStdinTTY, isStderrTTY, selectOne
	isStdinTTY = func() bool { return stdinTTY }
	isStderrTTY = func() bool { return stderrTTY }
	if pick != nil {
		selectOne = pick
	}
	return func() {
		isStdinTTY = origStdin
		isStderrTTY = origStderr
		selectOne = origSelect
	}
}

func TestPick_ZeroPMOXVMs(t *testing.T) {
	client := clusterServer(t, noTaggedFixture)
	restore := withPickerStubs(t, true, true, nil)
	defer restore()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrNoPMOXVMs) {
		t.Fatalf("err = %v, want ErrNoPMOXVMs", err)
	}
}

func TestPick_SinglePMOXVM_AutoSelect(t *testing.T) {
	client := clusterServer(t, oneTaggedFixture)
	pickerCalled := false
	restore := withPickerStubs(t, true, true, func(string, []huh.Option[string], string) string {
		pickerCalled = true
		return ""
	})
	defer restore()

	ref, err := Pick(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if pickerCalled {
		t.Error("picker should not run when exactly one VM exists")
	}
	if ref.Name != "smoke" || ref.VMID != 100 {
		t.Errorf("ref = %+v, want smoke/100", ref)
	}
}

func TestPick_SingleVM_AutoSelectEvenWithoutTTY(t *testing.T) {
	client := clusterServer(t, oneTaggedFixture)
	restore := withPickerStubs(t, false, false, nil)
	defer restore()

	ref, err := Pick(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if ref.VMID != 100 {
		t.Errorf("vmid = %d, want 100", ref.VMID)
	}
}

func TestPick_MultiVM_WithTTY_UsesPicker(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	var gotTitle string
	var gotOpts []huh.Option[string]
	restore := withPickerStubs(t, true, true, func(title string, opts []huh.Option[string], fallback string) string {
		gotTitle = title
		gotOpts = opts
		// Return the second option's value — should be VMID 200.
		return opts[1].Value
	})
	defer restore()

	ref, err := Pick(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if ref.VMID != 200 {
		t.Errorf("vmid = %d, want 200", ref.VMID)
	}
	if gotTitle == "" {
		t.Error("picker title was empty")
	}
	if len(gotOpts) != 2 {
		t.Fatalf("opts = %d, want 2 (only pmox-tagged VMs)", len(gotOpts))
	}
	// First option should be VMID 100 (alpha) since we sort ascending.
	if gotOpts[0].Value != "100" {
		t.Errorf("opts[0].Value = %q, want 100", gotOpts[0].Value)
	}
	if gotOpts[1].Value != "200" {
		t.Errorf("opts[1].Value = %q, want 200", gotOpts[1].Value)
	}
}

func TestPick_MultiVM_NoStdinTTY(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	restore := withPickerStubs(t, false, true, nil)
	defer restore()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrPickerNonTTY) {
		t.Fatalf("err = %v, want ErrPickerNonTTY", err)
	}
}

func TestPick_MultiVM_NoStderrTTY(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	restore := withPickerStubs(t, true, false, nil)
	defer restore()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrPickerNonTTY) {
		t.Fatalf("err = %v, want ErrPickerNonTTY", err)
	}
}

func TestPick_UserAborts(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	restore := withPickerStubs(t, true, true, func(string, []huh.Option[string], string) string {
		return "" // simulate SelectOne returning the fallback on abort
	})
	defer restore()

	_, err := Pick(context.Background(), client, nil)
	if err == nil {
		t.Fatal("expected error on abort")
	}
}
