package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/tui"
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

type pickFn func(string, []huh.Option[string]) (string, error)

// withPickerStubs overrides the TTY/no-input/picker injection points and
// restores them via the returned func.
func withPickerStubs(t *testing.T, stdinTTY, stderrTTY, noInputVal bool, pick pickFn) func() {
	t.Helper()
	origStdin, origStderr, origNoInput, origSelect := isStdinTTY, isStderrTTY, noInput, selectOne
	isStdinTTY = func() bool { return stdinTTY }
	isStderrTTY = func() bool { return stderrTTY }
	noInput = func() bool { return noInputVal }
	if pick != nil {
		selectOne = pick
	}
	return func() {
		isStdinTTY = origStdin
		isStderrTTY = origStderr
		noInput = origNoInput
		selectOne = origSelect
	}
}

func TestPick_ZeroPMOXVMs(t *testing.T) {
	client := clusterServer(t, noTaggedFixture)
	defer withPickerStubs(t, true, true, false, nil)()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrNoPMOXVMs) {
		t.Fatalf("err = %v, want ErrNoPMOXVMs", err)
	}
}

func TestPick_SinglePMOXVM_AutoSelect(t *testing.T) {
	client := clusterServer(t, oneTaggedFixture)
	pickerCalled := false
	defer withPickerStubs(t, true, true, false, func(string, []huh.Option[string]) (string, error) {
		pickerCalled = true
		return "", nil
	})()

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
	defer withPickerStubs(t, false, false, false, nil)()

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
	var gotOpts []huh.Option[string]
	defer withPickerStubs(t, true, true, false, func(_ string, opts []huh.Option[string]) (string, error) {
		gotOpts = opts
		return opts[0].Value, nil
	})()

	ref, err := Pick(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if len(gotOpts) != 2 {
		t.Fatalf("opts = %d, want 2 (only pmox-tagged VMs)", len(gotOpts))
	}
	// Running VMs sort first: beta (200, running) before alpha (100, stopped).
	if gotOpts[0].Value != "200" {
		t.Errorf("opts[0].Value = %q, want 200 (running sorts first)", gotOpts[0].Value)
	}
	if gotOpts[1].Value != "100" {
		t.Errorf("opts[1].Value = %q, want 100", gotOpts[1].Value)
	}
	if ref.VMID != 200 {
		t.Errorf("vmid = %d, want 200", ref.VMID)
	}
}

func TestPick_MultiVM_NoStdinTTY(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	defer withPickerStubs(t, false, true, false, nil)()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrPickerNonTTY) {
		t.Fatalf("err = %v, want ErrPickerNonTTY", err)
	}
	// The error should enumerate the available VMs so scripts don't need
	// a second `pmox list`.
	if !strings.Contains(err.Error(), "beta") || !strings.Contains(err.Error(), "alpha") {
		t.Errorf("non-TTY error should list candidates, got: %v", err)
	}
}

func TestPick_MultiVM_NoStderrTTY(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	defer withPickerStubs(t, true, false, false, nil)()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrPickerNonTTY) {
		t.Fatalf("err = %v, want ErrPickerNonTTY", err)
	}
}

func TestPick_MultiVM_NoInputDisablesPicker(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	// TTYs present, but --no-input / PMOX_NO_INPUT is set.
	defer withPickerStubs(t, true, true, true, func(string, []huh.Option[string]) (string, error) {
		t.Fatal("picker must not run when input is disabled")
		return "", nil
	})()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, ErrPickerNonTTY) {
		t.Fatalf("err = %v, want ErrPickerNonTTY when no-input", err)
	}
}

func TestPick_UserAborts(t *testing.T) {
	client := clusterServer(t, threeTaggedFixture)
	defer withPickerStubs(t, true, true, false, func(string, []huh.Option[string]) (string, error) {
		return "", tui.ErrCancelled
	})()

	_, err := Pick(context.Background(), client, nil)
	if !errors.Is(err, tui.ErrCancelled) {
		t.Fatalf("expected ErrCancelled on abort, got %v", err)
	}
}
