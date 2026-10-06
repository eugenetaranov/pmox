package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

// End-to-end flows through a real tea.Program fed real keystrokes, so
// huh's embedded forms, the shell's key routing and the async ops are
// exercised together exactly as on a terminal (minus the TTY).

func startE2E(t *testing.T, ops *stubOps) *teatest.TestModel {
	t.Helper()
	isolate(t)
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAA e2e@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	st := newWizState(cfg)
	st.ops = ops
	m := wizard.New(context.Background(), []wizard.Stage{
		&connectionStage{st: st}, &defaultsStage{st: st}, &accessStage{st: st}, &reviewStage{st: st}, &saveStage{st: st},
	}, wizard.Options{Hub: "review"})
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 50))
}

// waitFor consumes output until s appears; output read by one call is
// gone for the next, so wait on one marker per screen.
func waitFor(t *testing.T, tm *teatest.TestModel, s string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(s)) },
		teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}

// key sends k and pauses briefly: huh moves focus to the next field via
// an async command, so a key sent back-to-back could still land on the
// field being left (a human never types that fast between fields).
func key(tm *teatest.TestModel, k tea.KeyType) {
	tm.Send(tea.KeyMsg{Type: k})
	time.Sleep(40 * time.Millisecond)
}

func enter(tm *teatest.TestModel) { key(tm, tea.KeyEnter) }

// fillConnection types a URL, switches the token source to "paste" and
// fills the token fields.
func fillConnection(tm *teatest.TestModel) {
	tm.Type("pve.home.lan")
	enter(tm)
	key(tm, tea.KeyDown) // Generate → Paste
	enter(tm)
	tm.Type("root@pam!pmox")
	enter(tm)
	tm.Type("sek")
	enter(tm)
}

func TestE2EHappyPath(t *testing.T) {
	tm := startE2E(t, oneNode())
	waitFor(t, tm, "Server URL and API credentials")
	fillConnection(tm)

	waitFor(t, tm, "vmbr0")  // Defaults page, discovery loaded
	for i := 0; i < 5; i++ { // node, template, storage, snippets, bridge
		enter(tm)
	}

	waitFor(t, tm, "SSH key, default user")
	enter(tm) // use an existing key
	enter(tm) // id_ed25519.pub
	enter(tm) // default user
	enter(tm) // node ssh user
	enter(tm) // password auth
	tm.Type("ssh-pass")
	enter(tm)

	waitFor(t, tm, "Confirm and write configuration")
	enter(tm) // Confirm

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*wizard.Model)
	if fm.Err() != nil {
		t.Fatalf("wizard err: %v", fm.Err())
	}
	cfg, _ := config.Load()
	srv := cfg.Servers[formURL]
	if srv == nil || srv.Template != "9000" || srv.Bridge != "vmbr0" || srv.User != "ubuntu" || srv.NodeSSH == nil {
		t.Fatalf("saved server wrong: %+v", srv)
	}
}

func TestE2EEscBackFromAccess(t *testing.T) {
	tm := startE2E(t, oneNode())
	waitFor(t, tm, "Server URL and API credentials")
	fillConnection(tm)
	waitFor(t, tm, "vmbr0")
	for i := 0; i < 5; i++ {
		enter(tm)
	}
	waitFor(t, tm, "SSH key, default user")
	key(tm, tea.KeyEsc)
	waitFor(t, tm, "Node · template")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*wizard.Model)
	if !errors.Is(fm.Err(), tui.ErrAborted) {
		t.Fatalf("err = %v", fm.Err())
	}
}

func TestE2ECtrlCMidProbe(t *testing.T) {
	ops := oneNode()
	ops.probeHang = true
	tm := startE2E(t, ops)
	waitFor(t, tm, "Server URL and API credentials")
	fillConnection(tm)
	waitFor(t, tm, "Checking pve.home.lan:8006")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*wizard.Model)
	if !errors.Is(fm.Err(), tui.ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", fm.Err())
	}
	cfg, _ := config.Load()
	if len(cfg.Servers) != 0 {
		t.Errorf("ctrl+c mid-probe wrote %+v", cfg.Servers)
	}
}
