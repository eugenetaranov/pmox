package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// forgetSeams stubs the VM lookup and the remote commands. present says
// whether the VM has devbox-setup's created.json; checkErr fails the check.
// It returns the commands run, and the shutdown count seen at forget time.
func forgetSeams(t *testing.T, f *fakePVE, present bool, checkErr error) (*[]string, *int32) {
	t.Helper()
	origT, origR := devboxForgetTargetFn, devboxRemoteFn
	t.Cleanup(func() { devboxForgetTargetFn, devboxRemoteFn = origT, origR })
	devboxForgetTargetFn = func(context.Context, *pveclient.Client, *vm.Ref, *deleteFlags) (*sshTarget, error) {
		return &sshTarget{IP: "10.0.0.5", User: "pmox", Name: "web1"}, nil
	}
	var ran []string
	shutdownsAtForget := int32(-1)
	devboxRemoteFn = func(_ context.Context, _ *sshTarget, command string, _ time.Duration) (string, error) {
		ran = append(ran, command)
		switch command {
		case devboxCreatedCheck:
			if checkErr != nil {
				return "", checkErr
			}
			if present {
				return "present\n", nil
			}
			return "", nil
		case devboxForgetCommand:
			shutdownsAtForget = atomic.LoadInt32(&f.shutdownHits)
			return "✓ removed the GitHub key 'web1 (pmox)'\n", nil
		}
		return "", errors.New("unexpected command " + command)
	}
	return &ran, &shutdownsAtForget
}

func TestDelete_ForgetFlagRunsBeforeShutdown(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	ran, atShutdown := forgetSeams(t, f, true, nil)

	cmd, _, errb := newTestDeleteCmd()
	err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true, forget: true, srv: &config.Server{}}, yesConfirmer)
	if err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if len(*ran) != 2 || (*ran)[1] != devboxForgetCommand {
		t.Fatalf("remote commands = %q, want the check then forget", *ran)
	}
	if *atShutdown != 0 {
		t.Errorf("forget ran after %d shutdowns, want before the first", *atShutdown)
	}
	if !strings.Contains(errb.String(), "removed the GitHub key") {
		t.Errorf("forget's output not shown:\n%s", errb.String())
	}
	if f.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1", f.deleteHits)
	}
}

func TestDelete_ForgetPromptDefaultYes(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	ran, _ := forgetSeams(t, f, true, nil)
	var asked string
	df := &deleteFlags{srv: &config.Server{}, askForget: func(_ context.Context, p string) (bool, error) { asked = p; return true, nil }}

	cmd, _, _ := newTestDeleteCmd()
	if err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, df, yesConfirmer); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if !strings.Contains(asked, "Remove the credentials devbox-setup created on web1 first? [Y/n]") {
		t.Errorf("prompt = %q", asked)
	}
	if len(*ran) != 2 {
		t.Errorf("remote commands = %q, want check + forget", *ran)
	}
}

func TestDelete_YesWithoutForgetHints(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	ran, _ := forgetSeams(t, f, true, nil)

	cmd, _, errb := newTestDeleteCmd()
	if err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true, srv: &config.Server{}}, yesConfirmer); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if len(*ran) != 1 {
		t.Errorf("remote commands = %q, want only the check", *ran)
	}
	if !strings.Contains(errb.String(), "--forget") {
		t.Errorf("want a hint naming --forget:\n%s", errb.String())
	}
}

func TestDelete_ForgetCheckFailureStillDeletes(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	forgetSeams(t, f, false, errors.New("timed out"))

	cmd, _, errb := newTestDeleteCmd()
	if err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true, forget: true, srv: &config.Server{}}, yesConfirmer); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if !strings.Contains(errb.String(), "deleting anyway") || f.deleteHits != 1 {
		t.Errorf("want a warning and the delete; deletes=%d stderr:\n%s", f.deleteHits, errb.String())
	}
}

func TestDelete_NoForgetAndStoppedSkip(t *testing.T) {
	f := newFakePVE(t)
	f.clusterBody = taggedRunningVM
	ran, _ := forgetSeams(t, f, true, nil)
	cmd, _, _ := newTestDeleteCmd()
	if err := executeDelete(cmd.Context(), cmd, f.client(), []string{"web1"}, &deleteFlags{yes: true, noForget: true, srv: &config.Server{}}, yesConfirmer); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if len(*ran) != 0 {
		t.Errorf("--no-forget ran %q", *ran)
	}

	f2 := newFakePVE(t)
	f2.clusterBody = taggedStoppedVM
	f2.vmStatus = "stopped"
	ran2, _ := forgetSeams(t, f2, true, nil)
	cmd2, _, _ := newTestDeleteCmd()
	if err := executeDelete(cmd2.Context(), cmd2, f2.client(), []string{"web1"}, &deleteFlags{yes: true, forget: true, srv: &config.Server{}}, yesConfirmer); err != nil {
		t.Fatalf("executeDelete: %v", err)
	}
	if len(*ran2) != 0 {
		t.Errorf("a stopped VM ran %q", *ran2)
	}
}

func TestDelete_ForgetFlagsExclusive(t *testing.T) {
	cmd := newDeleteCmd()
	cmd.SetArgs([]string{"--forget", "--no-forget", "web1"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "forget") {
		t.Errorf("want an error for --forget with --no-forget, got %v", err)
	}
}
