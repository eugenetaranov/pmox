package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

func TestAccessDiffAndAffected(t *testing.T) {
	w := &accessWiz{
		acc: &accessreg.Access{People: map[string]*accessreg.Grant{
			"bob":   {VMs: []int{101}},
			"carol": {AllVMs: true},
		}},
		vms:    []pveclient.Resource{{VMID: 101, Name: "web1"}, {VMID: 102, Name: "db1"}, {VMID: 103, Name: "old"}},
		people: []string{"bob", "carol", "dave"},
		choice: map[string][]string{"bob": {"102"}, "carol": {"103"}, "dave": nil},
	}
	got := strings.Join(w.diffLines(), "\n")
	for _, want := range []string{"+ bob → db1", "− bob → web1", "− carol → all pmox VMs", "+ carol → old"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dave") {
		t.Errorf("dave had nothing and still has nothing — no diff expected:\n%s", got)
	}
	if n := len(w.affectedVMs()); n != 3 {
		t.Errorf("carol losing all-VMs access affects every VM; got %d", n)
	}
}

func newAccessHarness(t *testing.T) (*harness, *accessWiz, []wizard.Stage) {
	t.Helper()
	env, err := openAccessEnv(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &accessWiz{env: env, label: "pve", choice: map[string][]string{}}
	stages := []wizard.Stage{&accessPeopleStage{w: w}, &accessVMsStage{w: w}, &accessReviewStage{w: w}, &accessApplyStage{w: w}}
	h := &harness{t: t, m: wizard.New(context.Background(), stages, wizard.Options{Hub: "review"})}
	h.run(h.m.Init())
	h.run(func() tea.Msg { return tea.WindowSizeMsg{Width: 160, Height: 50} })
	return h, w, stages
}

func TestAccessWizardGrantFlow(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)
	publishAs(t.Context(), t, reg, "carol", testKeyB)
	h, _, stages := newAccessHarness(t)
	people, vms, review := stages[0].(*accessPeopleStage), stages[1].(*accessVMsStage), stages[2].(*accessReviewStage)

	h.expectStage("people")
	if v := h.m.View(); !strings.Contains(v, "bob") || !strings.Contains(v, "carol") {
		t.Fatalf("people page:\n%s", v)
	}
	people.selected = []string{"bob", "carol"}
	h.run(people.submit())
	h.expectStage("vms")
	if !strings.Contains(h.m.View(), "VMs bob can reach  (1 of 2)") {
		t.Errorf("vms page:\n%s", h.m.View())
	}
	vms.selected = []string{"101", "102"}
	h.run(vms.submit())
	h.expectStage("vms") // carol's page
	vms.selected = []string{allVMsValue}
	h.run(vms.submit())
	h.expectStage("review")
	v := h.m.View()
	for _, want := range []string{"+ bob → web1, db1", "+ carol → all pmox VMs"} {
		if !strings.Contains(v, want) {
			t.Errorf("review missing %q:\n%s", want, v)
		}
	}
	review.action = "apply"
	h.run(review.submit())
	if !h.m.Done() || h.m.Err() != nil {
		t.Fatalf("done=%v err=%v", h.m.Done(), h.m.Err())
	}
	acc, _ := accessreg.ReadAccess(context.Background(), reg)
	if !acc.Allowed("bob", 102) || acc.Allowed("bob", 103) || !acc.Allowed("carol", 103) {
		t.Errorf("registry = bob %+v carol %+v", acc.People["bob"], acc.People["carol"])
	}
	if got := strings.Join(guests.managed(101), ","); got != "bob,carol" {
		t.Errorf("web1 managed = %s", got)
	}
	summary := ""
	for _, l := range h.m.Result().Summary {
		summary += l.Text + "\n"
	}
	if !strings.Contains(summary, "✓ web1 (101)") || !strings.Contains(summary, "… old (103): stopped") {
		t.Errorf("summary:\n%s", summary)
	}
}

func TestAccessWizardEmptyRegistry(t *testing.T) {
	setupAccessEnv(t)
	h, _, _ := newAccessHarness(t)
	if !strings.Contains(h.m.View(), "pmox key publish") {
		t.Fatalf("empty state should explain publishing:\n%s", h.m.View())
	}
	h.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !errors.Is(h.m.Err(), exitcode.ErrNotFound) {
		t.Errorf("err = %v", h.m.Err())
	}
}

func TestAccessWizardE2E(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)
	env, err := openAccessEnv(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &accessWiz{env: env, label: "pve", choice: map[string][]string{}}
	m := wizard.New(context.Background(), []wizard.Stage{
		&accessPeopleStage{w: w}, &accessVMsStage{w: w}, &accessReviewStage{w: w}, &accessApplyStage{w: w},
	}, wizard.Options{Hub: "review", Title: "pmox access"})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))
	wait := func(s string) {
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(s)) },
			teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
	}
	press := func(k tea.KeyMsg) { tm.Send(k); time.Sleep(40 * time.Millisecond) }
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	wait("Who should have access?")
	press(enter) // bob is preselected (the only published key)
	wait("VMs bob can reach")
	press(tea.KeyMsg{Type: tea.KeyDown})                      // ★ all → db1 (sorted: db1, old, web1)
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // select db1
	press(enter)
	wait("+ bob → db1")
	press(enter) // Apply
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*wizard.Model)
	if fm.Err() != nil {
		t.Fatalf("err = %v", fm.Err())
	}
	if got := guests.managed(102); len(got) != 1 || got[0] != "bob" {
		t.Errorf("db1 managed = %v", got)
	}
}
