package main

import (
	"context"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
)

const formURL = "https://pve.home.lan:8006/api2/json"

// stubFormSeams replaces the init form seams with canned collectors and the
// given sequence of review actions, restoring them on cleanup.
func stubFormSeams(t *testing.T, reviewActions ...string) *int {
	t.Helper()
	origEst, origDef, origAcc, origRev, origInter := establishConnectionFn, collectDefaultsFn, collectAccessFn, reviewFn, interactiveFn
	interactiveFn = func() bool { return true }
	defaultsCalls := 0

	establishConnectionFn = func(_ context.Context, _ prompter, _ *config.Config, _ connInputs) (resolvedConn, connInputs, error) {
		return resolvedConn{canonical: formURL, tokenID: "root@pam!pmox", secret: "sek", insecure: false}, connInputs{}, nil
	}
	collectDefaultsFn = func(_ context.Context, _ prompter, _ resolvedConn, _ defaultsAnswers, _ bool) (defaultsAnswers, error) {
		defaultsCalls++
		return defaultsAnswers{node: "pve", template: "9000", storage: "local-lvm", snippetStorage: "local", bridge: "vmbr0"}, nil
	}
	collectAccessFn = func(_ context.Context, _ prompter, _ string, _ accessAnswers, _ bool) (accessAnswers, error) {
		return accessAnswers{sshKey: "/tmp/none.pub", user: "ubuntu", nodeSSH: &config.NodeSSH{User: "root", Auth: "key", KeyPath: "/tmp/none"}}, nil
	}
	i := 0
	reviewFn = func(_ prompter, _ []string) (string, error) {
		a := reviewActions[len(reviewActions)-1]
		if i < len(reviewActions) {
			a = reviewActions[i]
		}
		i++
		return a, nil
	}
	t.Cleanup(func() {
		establishConnectionFn, collectDefaultsFn, collectAccessFn, reviewFn, interactiveFn = origEst, origDef, origAcc, origRev, origInter
	})
	return &defaultsCalls
}

func TestRunInteractiveFormConfirmWrites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubFormSeams(t, "confirm")

	p := &fakePrompter{}
	if err := runInteractive(context.Background(), p); err != nil {
		t.Fatalf("runInteractive(form): %v", err)
	}
	cfg, _ := config.Load()
	srv := cfg.Servers[formURL]
	if srv == nil {
		t.Fatalf("server not written; config: %+v", cfg.Servers)
	}
	if srv.TokenID != "root@pam!pmox" || srv.Node != "pve" || srv.Template != "9000" || srv.Bridge != "vmbr0" {
		t.Errorf("server fields wrong: %+v", srv)
	}
	if sec, err := credstore.Get(formURL); err != nil || sec != "sek" {
		t.Errorf("secret not stored: %q %v", sec, err)
	}
}

func TestRunInteractiveFormBackToDefaultsThenConfirm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	calls := stubFormSeams(t, "defaults", "confirm") // go back to defaults once, then confirm

	p := &fakePrompter{}
	if err := runInteractive(context.Background(), p); err != nil {
		t.Fatalf("runInteractive(form): %v", err)
	}
	// collectDefaults runs once on the first pass, again after "defaults".
	if *calls != 2 {
		t.Errorf("collectDefaults called %d times, want 2", *calls)
	}
	if _, err := config.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg, _ := config.Load()
	if cfg.Servers[formURL] == nil {
		t.Error("server not written after back-then-confirm")
	}
}

func TestRunInteractiveFormCancelWritesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubFormSeams(t, "cancel") // unknown action → treated as cancel

	p := &fakePrompter{}
	if err := runInteractive(context.Background(), p); err == nil {
		t.Fatal("want error on cancel")
	}
	cfg, _ := config.Load()
	if len(cfg.Servers) != 0 {
		t.Errorf("cancel should write nothing, got %+v", cfg.Servers)
	}
}
