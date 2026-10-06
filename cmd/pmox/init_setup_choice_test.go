package main

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

func stubSetupChoice(t *testing.T, choice string) *string {
	t.Helper()
	var title string
	orig := setupChoiceFn
	setupChoiceFn = func(tt string, _ []huh.Option[string]) (string, error) { title = tt; return choice, nil }
	t.Cleanup(func() { setupChoiceFn = orig })
	return &title
}

func configuredOnce(t *testing.T) {
	t.Helper()
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox", Node: "pve"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestInitWhenConfiguredOffersChoicesNotURL(t *testing.T) {
	configuredOnce(t)
	stubProbe(t, true, 0) // interactive
	title := stubSetupChoice(t, "cancel")
	p := &fakePrompter{}
	if err := runInitConfigured(context.Background(), p, mustLoad(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*title, "already set up for pve.home.lan") {
		t.Errorf("title = %q", *title)
	}
	if strings.Contains(p.out.String(), "Proxmox API URL") {
		t.Error("must not prompt for the URL again")
	}
}

func TestInitWhenConfiguredAddRunsWizard(t *testing.T) {
	configuredOnce(t)
	stubSetupChoice(t, "add")
	ran := ""
	stubRunWizard(t, func(_ []wizard.Stage, opts wizard.Options) (wizard.Result, error) {
		ran = opts.Start
		return wizard.Result{}, nil
	})
	if err := runInitConfigured(context.Background(), &fakePrompter{}, mustLoad(t)); err != nil {
		t.Fatal(err)
	}
	if ran != "connection" {
		t.Errorf("add should start the wizard at Connection, got %q", ran)
	}
}

func TestInitWhenConfiguredEditStartsOnReview(t *testing.T) {
	configuredOnce(t)
	stubSetupChoice(t, "edit")
	orig := establishEditConnectionFn
	establishEditConnectionFn = func(_ context.Context, _ prompter, _ *config.Config, c string) (resolvedConn, error) {
		return resolvedConn{canonical: c}, nil
	}
	t.Cleanup(func() { establishEditConnectionFn = orig })
	ran := ""
	stubRunWizard(t, func(_ []wizard.Stage, opts wizard.Options) (wizard.Result, error) {
		ran = opts.Start
		return wizard.Result{}, nil
	})
	if err := runInitConfigured(context.Background(), &fakePrompter{}, mustLoad(t)); err != nil {
		t.Fatal(err)
	}
	if ran != "review" {
		t.Errorf("edit should open the wizard on Review, got %q", ran)
	}
}

func TestInitWhenConfiguredRemoveAsksFirst(t *testing.T) {
	configuredOnce(t)
	stubSetupChoice(t, "remove")
	orig := confirmRemoveFn
	confirmRemoveFn = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { confirmRemoveFn = orig })
	if err := runInitConfigured(context.Background(), &fakePrompter{}, mustLoad(t)); err != nil {
		t.Fatal(err)
	}
	if cfg := mustLoad(t); len(cfg.Servers) != 1 {
		t.Error("declining must remove nothing")
	}
	confirmRemoveFn = func(string, bool) (bool, error) { return true, nil }
	if err := runInitConfigured(context.Background(), &fakePrompter{}, mustLoad(t)); err != nil {
		t.Fatal(err)
	}
	if cfg := mustLoad(t); len(cfg.Servers) != 0 {
		t.Errorf("server not removed: %+v", cfg.Servers)
	}
}

func mustLoad(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
