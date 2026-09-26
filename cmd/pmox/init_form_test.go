package main

import (
	"context"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
)

const formURL = "https://pve.home.lan:8006/api2/json"

// stubFormSeams replaces the init form seams with canned collectors and the
// given sequence of review actions, restoring them on cleanup. Returns call
// counters for collectDefaults and collectAccess so tests can assert on
// stage re-entry.
func stubFormSeams(t *testing.T, reviewActions ...string) (defaultsCalls, accessCalls *int) {
	t.Helper()
	origEst, origDef, origAcc, origRev, origInter := establishConnectionFn, collectDefaultsFn, collectAccessFn, reviewFn, interactiveFn
	interactiveFn = func() bool { return true }
	defaultsCalls, accessCalls = new(int), new(int)

	establishConnectionFn = func(_ context.Context, _ prompter, _ *config.Config, _ connInputs, _ map[string]bool) (resolvedConn, connInputs, error) {
		return resolvedConn{canonical: formURL, tokenID: "root@pam!pmox", secret: "sek", insecure: false}, connInputs{}, nil
	}
	collectDefaultsFn = func(_ context.Context, _ prompter, _ resolvedConn, _ defaultsAnswers, _ bool) (defaultsAnswers, error) {
		*defaultsCalls++
		return defaultsAnswers{node: "pve", template: "9000", storage: "local-lvm", snippetStorage: "local", bridge: "vmbr0"}, nil
	}
	collectAccessFn = func(_ context.Context, _ prompter, _ *config.Config, _ string, _ accessAnswers, _ bool) (accessAnswers, error) {
		*accessCalls++
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
	return defaultsCalls, accessCalls
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
	defaultsCalls, accessCalls := stubFormSeams(t, "defaults", "confirm") // go back to defaults once, then confirm

	p := &fakePrompter{}
	if err := runInteractive(context.Background(), p); err != nil {
		t.Fatalf("runInteractive(form): %v", err)
	}
	// collectDefaults runs once on the first pass, again after "defaults".
	if *defaultsCalls != 2 {
		t.Errorf("collectDefaults called %d times, want 2", *defaultsCalls)
	}
	// Once Review has been reached, editing Defaults must return straight
	// to Review rather than cascading forward through Access again.
	if *accessCalls != 1 {
		t.Errorf("collectAccess called %d times, want 1 (editing defaults shouldn't re-run access)", *accessCalls)
	}
	if _, err := config.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg, _ := config.Load()
	if cfg.Servers[formURL] == nil {
		t.Error("server not written after back-then-confirm")
	}
}

func TestRunInteractiveFormBackToAccessThenConfirm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defaultsCalls, accessCalls := stubFormSeams(t, "access", "confirm") // go back to access once, then confirm

	p := &fakePrompter{}
	if err := runInteractive(context.Background(), p); err != nil {
		t.Fatalf("runInteractive(form): %v", err)
	}
	if *defaultsCalls != 1 {
		t.Errorf("collectDefaults called %d times, want 1 (editing access shouldn't re-run defaults)", *defaultsCalls)
	}
	if *accessCalls != 2 {
		t.Errorf("collectAccess called %d times, want 2", *accessCalls)
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

// stubEditFormSeams is stubFormSeams plus establishEditConnectionFn,
// seeded to reuse formURL's already-configured token as-is (no
// re-typing) — matching what establishEditConnection actually does.
func stubEditFormSeams(t *testing.T, reviewActions ...string) (defaultsCalls, accessCalls *int) {
	t.Helper()
	defaultsCalls, accessCalls = stubFormSeams(t, reviewActions...)
	origEdit := establishEditConnectionFn
	establishEditConnectionFn = func(_ context.Context, _ prompter, _ *config.Config, canonical string) (resolvedConn, error) {
		return resolvedConn{canonical: canonical, tokenID: "root@pam!pmox", secret: "sek", insecure: false}, nil
	}
	t.Cleanup(func() { establishEditConnectionFn = origEdit })
	return defaultsCalls, accessCalls
}

func seedEditableServer(t *testing.T) {
	t.Helper()
	cfg := &config.Config{Servers: map[string]*config.Server{
		formURL: {
			TokenID: "root@pam!pmox", Node: "pve", Template: "9000",
			Storage: "local-lvm", SnippetStorage: "local", Bridge: "vmbr0",
		},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// runEditForm must land directly on Review — never Connection, Defaults,
// or Access — since its whole point is skipping the parts that haven't
// changed. Confirming immediately persists the pre-seeded (current)
// values unchanged.
func TestRunEditForm_StartsAtReviewAndConfirmWrites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedEditableServer(t)
	defaultsCalls, accessCalls := stubEditFormSeams(t, "confirm")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := runEditForm(context.Background(), &fakePrompter{}, cfg, formURL); err != nil {
		t.Fatalf("runEditForm: %v", err)
	}
	if *defaultsCalls != 0 || *accessCalls != 0 {
		t.Errorf("collectDefaults/collectAccess called %d/%d times, want 0/0 (edit landed straight on review)", *defaultsCalls, *accessCalls)
	}
	reloaded, _ := config.Load()
	srv := reloaded.Servers[formURL]
	if srv == nil || srv.Template != "9000" || srv.Storage != "local-lvm" {
		t.Errorf("server after confirm-without-editing = %+v, want the original values unchanged", srv)
	}
}

// Revisiting Defaults from Review's pre-seeded state must offer the
// server's current values (not a blank re-discovery) — collectDefaults
// receives them as its prev/haveDefs args, which the stub in
// stubFormSeams doesn't itself inspect, but the seeding here proves
// runEditForm actually populated formState.defs before the loop starts.
func TestRunEditForm_CanRevisitDefaultsFromPrefilledReview(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedEditableServer(t)
	defaultsCalls, accessCalls := stubEditFormSeams(t, "defaults", "confirm")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := runEditForm(context.Background(), &fakePrompter{}, cfg, formURL); err != nil {
		t.Fatalf("runEditForm: %v", err)
	}
	if *defaultsCalls != 1 {
		t.Errorf("collectDefaults called %d times, want 1", *defaultsCalls)
	}
	if *accessCalls != 0 {
		t.Errorf("collectAccess called %d times, want 0 (editing defaults shouldn't re-run access)", *accessCalls)
	}
}
