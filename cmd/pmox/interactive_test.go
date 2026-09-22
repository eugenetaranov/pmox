package main

import (
	"context"
	"io"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// stubVMPick is defined in ssh_test.go (same package).

func TestEnsureVMRef_ExplicitPassthrough(t *testing.T) {
	// An explicit ref must never trigger the picker.
	orig := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client, io.Writer) (*vm.Ref, error) {
		t.Fatal("picker must not run when a VM ref is given")
		return nil, nil
	}
	t.Cleanup(func() { vmPickFn = orig })

	got, err := ensureVMRef(context.Background(), newTestUmountCmd(), nil, "web1")
	if err != nil || got != "web1" {
		t.Fatalf("ensureVMRef(explicit) = %q, %v; want web1, nil", got, err)
	}
}

func TestEnsureVMRef_PicksWhenEmpty(t *testing.T) {
	stubVMPick(t, &vm.Ref{VMID: 104, Name: "web1"}, nil)
	got, err := ensureVMRef(context.Background(), newTestUmountCmd(), nil, "")
	if err != nil {
		t.Fatalf("ensureVMRef: %v", err)
	}
	if got != "104" {
		t.Errorf("ensureVMRef(empty) = %q, want 104 (picked VMID)", got)
	}
}
