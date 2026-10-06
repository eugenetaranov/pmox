package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/exitcode"
)

func exitErr(code int) error {
	return exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
}

func stubAuthRejected(t *testing.T, rejected bool) *int {
	t.Helper()
	probes := 0
	orig := sshAuthRejectedFn
	sshAuthRejectedFn = func(context.Context, *sshTarget) bool { probes++; return rejected }
	t.Cleanup(func() { sshAuthRejectedFn = orig })
	return &probes
}

func TestExplainSSHFailureOnlyWhenKeyRejected(t *testing.T) {
	ctx := context.Background()
	target := &sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/nonexistent/key", VMID: 101, Name: "web1"}

	probes := stubAuthRejected(t, true)
	boom := errors.New("fork failed")
	if got := explainSSHFailure(ctx, target, formURL, boom, true); !errors.Is(got, boom) {
		t.Errorf("non-exit error must pass through, got %v", got)
	}
	remote := exitErr(1)
	if got := explainSSHFailure(ctx, target, formURL, remote, true); !errors.Is(got, remote) || *probes != 0 {
		t.Errorf("ssh exit 1 is the remote command's status — no probe expected (probes=%d)", *probes)
	}

	origStatus, origUser := registryStatusFn, localUsername
	t.Cleanup(func() { registryStatusFn, localUsername = origStatus, origUser })
	for status, want := range map[regStatus]string{
		regUnknown:      "pmox key publish",
		regNotPublished: "Publish your key once:   pmox key publish",
		regNotGranted:   `published as "bob" but not granted for web1`,
		regGranted:      "pmox access sync web1",
		regDifferentKey: "pmox key publish --replace",
	} {
		registryStatusFn = func(context.Context, string, string, string, int) regStatus { return status }
		localUsername = func() (string, error) { return "bob", nil }
		got := explainSSHFailure(ctx, target, formURL, exitErr(255), true)
		var ae *sshAuthError
		if !errors.As(got, &ae) || exitcode.From(got) != exitcode.ExitSSHAuth {
			t.Fatalf("status %d: got %v (exit %d)", status, got, exitcode.From(got))
		}
		if !strings.Contains(ae.msg, want) || !strings.Contains(ae.msg, "web1 doesn't accept your SSH key") {
			t.Errorf("status %d: message missing %q:\n%s", status, want, ae.msg)
		}
	}
	if *probes == 0 {
		t.Error("exit 255 should have probed")
	}
}

func TestExplainSSHFailureNotRejectedPassesThrough(t *testing.T) {
	stubAuthRejected(t, false)
	err := exitErr(255)
	if got := explainSSHFailure(context.Background(), &sshTarget{}, formURL, err, true); !errors.Is(got, err) {
		t.Errorf("unreachable host (not a key rejection) must pass through, got %v", got)
	}
}

func TestRegistryStatus(t *testing.T) {
	ctx := context.Background()
	reg, _ := setupAccessEnv(t)
	withNodeSSH(t)
	mine, _ := accessreg.NewPublishedKey("bob", testKeyA)

	if got := registryStatus(ctx, formURL, "bob", mine.Fingerprint, 101); got != regNotPublished {
		t.Errorf("unpublished: got %d", got)
	}
	publishAs(ctx, t, reg, "bob", testKeyB)
	if got := registryStatus(ctx, formURL, "bob", mine.Fingerprint, 101); got != regDifferentKey {
		t.Errorf("different key: got %d", got)
	}
	publishAs(ctx, t, reg, "bob", testKeyA)
	if got := registryStatus(ctx, formURL, "bob", mine.Fingerprint, 101); got != regNotGranted {
		t.Errorf("not granted: got %d", got)
	}
	_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantVMs("bob", 101); return nil })
	if got := registryStatus(ctx, formURL, "bob", mine.Fingerprint, 101); got != regGranted {
		t.Errorf("granted: got %d", got)
	}
}
