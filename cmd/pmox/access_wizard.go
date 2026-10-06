package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

// The interactive 'pmox access setup': People › VMs › Review, then a hidden
// apply step that writes access.yaml and syncs the affected VMs.

const allVMsValue = "*"

// accessWiz is the state the access stages share.
type accessWiz struct {
	env    *accessEnv
	label  string
	keys   []accessreg.PublishedKey
	acc    *accessreg.Access
	vms    []pveclient.Resource
	loaded bool

	people []string            // selected names, in order
	choice map[string][]string // name → selected values ("*" or VMIDs)
}

// desiredGrant turns a VMs-page selection into a grant.
func desiredGrant(values []string) *accessreg.Grant {
	g := &accessreg.Grant{}
	for _, v := range values {
		if v == allVMsValue {
			return &accessreg.Grant{AllVMs: true}
		}
		if id, err := strconv.Atoi(v); err == nil {
			g.VMs = append(g.VMs, id)
		}
	}
	sort.Ints(g.VMs)
	return g
}

// accessChange is one person's before/after.
type accessChange struct {
	name          string
	before, after *accessreg.Grant
}

func (w *accessWiz) changes() []accessChange {
	var out []accessChange
	for _, n := range w.people {
		before := w.acc.People[n]
		if before == nil {
			before = &accessreg.Grant{}
		}
		after := desiredGrant(w.choice[n])
		if before.AllVMs == after.AllVMs && slices.Equal(sortedCopy(before.VMs), after.VMs) {
			continue
		}
		out = append(out, accessChange{name: n, before: before, after: after})
	}
	return out
}

func sortedCopy(v []int) []int {
	c := slices.Clone(v)
	sort.Ints(c)
	return c
}

func (w *accessWiz) vmName(id int) string {
	for _, r := range w.vms {
		if r.VMID == id {
			return r.Name
		}
	}
	return fmt.Sprintf("vmid %d (gone)", id)
}

// diffLines renders changes as "+ bob → web1" / "− carol → db1" lines.
func (w *accessWiz) diffLines() []string {
	var lines []string
	for _, c := range w.changes() {
		switch {
		case c.after.AllVMs && !c.before.AllVMs:
			lines = append(lines, fmt.Sprintf("+ %s → all pmox VMs (including future ones)", c.name))
		case c.before.AllVMs && !c.after.AllVMs:
			lines = append(lines, fmt.Sprintf("− %s → all pmox VMs", c.name))
		}
		if c.after.AllVMs {
			continue
		}
		var add, del []string
		for _, id := range c.after.VMs {
			if c.before.AllVMs || !slices.Contains(c.before.VMs, id) {
				add = append(add, w.vmName(id))
			}
		}
		if !c.before.AllVMs {
			for _, id := range c.before.VMs {
				if !slices.Contains(c.after.VMs, id) {
					del = append(del, w.vmName(id))
				}
			}
		}
		if len(add) > 0 {
			lines = append(lines, fmt.Sprintf("+ %s → %s", c.name, strings.Join(add, ", ")))
		}
		if len(del) > 0 {
			lines = append(lines, fmt.Sprintf("− %s → %s", c.name, strings.Join(del, ", ")))
		}
	}
	return lines
}

// affectedVMs are the VMs whose managed block may change.
func (w *accessWiz) affectedVMs() []pveclient.Resource {
	all := false
	ids := map[int]bool{}
	for _, c := range w.changes() {
		if c.before.AllVMs || c.after.AllVMs {
			all = true
		}
		for _, id := range c.before.VMs {
			ids[id] = true
		}
		for _, id := range c.after.VMs {
			ids[id] = true
		}
	}
	var out []pveclient.Resource
	for _, r := range w.vms {
		if all || ids[r.VMID] {
			out = append(out, r)
		}
	}
	return out
}

// runAccessWizard opens the registry for the resolved context (the
// context picker, when several are configured, runs first) and starts
// the wizard.
func runAccessWizard(cmd *cobra.Command) error {
	ctx := cmd.Context()
	env, err := openAccessEnv(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()
	w := &accessWiz{env: env, label: targetLabel(env.cfg, env.resolved), choice: map[string][]string{}}
	stages := []wizard.Stage{
		&accessPeopleStage{w: w}, &accessVMsStage{w: w}, &accessReviewStage{w: w}, &accessApplyStage{w: w},
	}
	res, err := runWizardFn(ctx, stages, wizard.Options{Title: "pmox access · " + w.label, AltScreen: true, Hub: "review"})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	failed := false
	for _, l := range res.Summary {
		if l.Warn {
			failed = true
			fmt.Fprintln(cmd.ErrOrStderr(), tui.Warnf(l.Text))
		} else {
			fmt.Fprintln(out, l.Text)
		}
	}
	if failed {
		return errAccessSync
	}
	return nil
}

// ---------------------------------------------------------------- People

type accessLoadedMsg struct {
	keys []accessreg.PublishedKey
	acc  *accessreg.Access
	vms  []pveclient.Resource
	err  error
}

type accessPeopleStage struct {
	w        *accessWiz
	selected []string
	form     *wizard.Form
	empty    bool
}

func (s *accessPeopleStage) ID() string       { return "people" }
func (s *accessPeopleStage) Title() string    { return "People" }
func (s *accessPeopleStage) Subtitle() string { return "Who should have access?" }

func (s *accessPeopleStage) View() string {
	if s.empty {
		return "Nobody has published a key on this cluster yet.\n\n" +
			"Each person runs, from their own account:\n\n" +
			"    pmox key publish\n\n" +
			"Then come back here to choose which VMs they can reach.\n\n" +
			tui.Subtitle("press enter or esc to close")
	}
	if s.form == nil {
		return ""
	}
	return s.form.View()
}

func (s *accessPeopleStage) Enter(ctx context.Context) tea.Cmd {
	if s.w.loaded {
		return s.newForm()
	}
	env := s.w.env
	return busyThen("Loading the access registry and VMs …", func() tea.Msg {
		keys, _, err := accessreg.ListKeys(ctx, env.fs)
		if err != nil {
			return accessLoadedMsg{err: err}
		}
		acc, err := accessreg.ReadAccess(ctx, env.fs)
		if err != nil {
			return accessLoadedMsg{err: err}
		}
		vms, err := pmoxVMs(ctx, env)
		return accessLoadedMsg{keys: keys, acc: acc, vms: vms, err: err}
	})
}

func (s *accessPeopleStage) newForm() tea.Cmd {
	if len(s.w.keys) == 0 {
		s.empty = true
		return wizard.Idle()
	}
	s.selected = slices.Clone(s.w.people)
	if len(s.selected) == 0 && len(s.w.keys) == 1 {
		s.selected = []string{s.w.keys[0].Name}
	}
	opts := make([]huh.Option[string], 0, len(s.w.keys))
	for _, k := range s.w.keys {
		label := fmt.Sprintf("%-12s %s   %s", k.Name, k.Fingerprint, grantSummary(s.w.acc.People[k.Name], s.w.vms))
		if k.Host != "" {
			label += tui.Subtitle("   from " + k.Host)
		}
		opts = append(opts, huh.NewOption(label, k.Name).Selected(slices.Contains(s.selected, k.Name)))
	}
	s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title("People (space to select)").Options(opts...).Value(&s.selected).
			Validate(func(v []string) error {
				if len(v) == 0 {
					return errors.New("select at least one person")
				}
				return nil
			}),
	)))
	return tea.Batch(wizard.Idle(), s.form.Init())
}

func (s *accessPeopleStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	if m, ok := msg.(accessLoadedMsg); ok {
		if m.err != nil {
			return s, wizard.Abort(m.err)
		}
		s.w.keys, s.w.acc, s.w.vms, s.w.loaded = m.keys, m.acc, m.vms, true
		return s, s.newForm()
	}
	if s.empty {
		if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyEnter {
			return s, wizard.Abort(fmt.Errorf("%w: no published keys on %s — people need to run 'pmox key publish' first", exitcode.ErrNotFound, s.w.label))
		}
		return s, nil
	}
	if s.form == nil {
		return s, nil
	}
	cmd, submitted := s.form.Update(msg)
	if submitted {
		return s, tea.Batch(cmd, s.submit())
	}
	return s, cmd
}

func (s *accessPeopleStage) submit() tea.Cmd {
	s.w.people = slices.Clone(s.selected)
	for _, n := range s.w.people {
		if _, ok := s.w.choice[n]; ok {
			continue
		}
		s.w.choice[n] = currentValues(s.w.acc.People[n])
	}
	return wizard.GoTo("vms")
}

// currentValues is the VMs-page preselection for an existing grant.
func currentValues(g *accessreg.Grant) []string {
	if g == nil {
		return nil
	}
	if g.AllVMs {
		return []string{allVMsValue}
	}
	vals := make([]string, 0, len(g.VMs))
	for _, id := range g.VMs {
		vals = append(vals, strconv.Itoa(id))
	}
	return vals
}

// ---------------------------------------------------------------- VMs

type accessVMsStage struct {
	w        *accessWiz
	idx      int
	selected []string
	form     *wizard.Form
}

func (s *accessVMsStage) ID() string    { return "vms" }
func (s *accessVMsStage) Title() string { return "VMs" }
func (s *accessVMsStage) Subtitle() string {
	if len(s.w.people) == 0 {
		return "Which VMs can they reach?"
	}
	return fmt.Sprintf("VMs %s can reach  (%d of %d)", s.w.people[s.idx], s.idx+1, len(s.w.people))
}

func (s *accessVMsStage) View() string {
	if s.form == nil {
		return ""
	}
	return s.form.View()
}

func (s *accessVMsStage) Enter(context.Context) tea.Cmd {
	s.idx = 0
	return s.newForm()
}

func (s *accessVMsStage) newForm() tea.Cmd {
	name := s.w.people[s.idx]
	s.selected = slices.Clone(s.w.choice[name])
	opts := []huh.Option[string]{
		huh.NewOption("★ All pmox VMs, including future ones", allVMsValue).Selected(slices.Contains(s.selected, allVMsValue)),
	}
	for _, r := range s.w.vms {
		v := strconv.Itoa(r.VMID)
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-20s %-6d %s", r.Name, r.VMID, r.Status), v).Selected(slices.Contains(s.selected, v)))
	}
	s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title(fmt.Sprintf("VMs for %s (space to toggle; none = no access)", name)).
			Options(opts...).Value(&s.selected),
	)))
	return s.form.Init()
}

func (s *accessVMsStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	if s.form == nil {
		return s, nil
	}
	cmd, submitted := s.form.Update(msg)
	if submitted {
		return s, tea.Batch(cmd, s.submit())
	}
	return s, cmd
}

func (s *accessVMsStage) submit() tea.Cmd {
	s.w.choice[s.w.people[s.idx]] = slices.Clone(s.selected)
	if s.idx+1 < len(s.w.people) {
		s.idx++
		return s.newForm()
	}
	return wizard.GoTo("review")
}

// ---------------------------------------------------------------- Review

type accessReviewStage struct {
	w      *accessWiz
	action string
	form   *wizard.Form
}

func (s *accessReviewStage) ID() string       { return "review" }
func (s *accessReviewStage) Title() string    { return "Review" }
func (s *accessReviewStage) Subtitle() string { return "Confirm the changes" }

func (s *accessReviewStage) Enter(context.Context) tea.Cmd {
	s.action = "apply"
	opts := []huh.Option[string]{
		huh.NewOption("Apply — update the registry and the VMs", "apply"),
		huh.NewOption("Edit people", "people"),
		huh.NewOption("Edit VMs", "vms"),
		huh.NewOption("Cancel — change nothing", "cancel"),
	}
	if len(s.w.changes()) == 0 {
		opts[0] = huh.NewOption("Done — nothing to change", "cancel")
		s.action = "cancel"
	}
	s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Apply these changes?").Options(opts...).Value(&s.action),
	)))
	return s.form.Init()
}

func (s *accessReviewStage) View() string {
	var b strings.Builder
	lines := s.w.diffLines()
	if len(lines) == 0 {
		b.WriteString("  No changes.\n")
	}
	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}
	if n := len(s.w.affectedVMs()); n > 0 {
		b.WriteString(tui.Subtitle(fmt.Sprintf("\n  %d VM(s) will be updated through the guest agent.", n)) + "\n")
	}
	b.WriteString("\n" + s.form.View())
	return b.String()
}

func (s *accessReviewStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	cmd, submitted := s.form.Update(msg)
	if !submitted {
		return s, cmd
	}
	return s, tea.Batch(cmd, s.submit())
}

func (s *accessReviewStage) submit() tea.Cmd {
	switch s.action {
	case "apply":
		return wizard.GoTo("apply")
	case "people", "vms":
		return wizard.GoTo(s.action)
	default:
		return wizard.Finish(wizard.Result{Summary: []wizard.Line{{Text: "no changes made"}}})
	}
}

// ---------------------------------------------------------------- Apply

type accessAppliedMsg struct {
	outcomes []syncOutcome
	err      error
}

type accessApplyStage struct {
	w       *accessWiz
	working bool
}

func (s *accessApplyStage) ID() string       { return "apply" }
func (s *accessApplyStage) Title() string    { return "Review" }
func (s *accessApplyStage) Subtitle() string { return "Applying changes" }
func (s *accessApplyStage) Hidden() bool     { return true }
func (s *accessApplyStage) Pinned() bool     { return s.working }
func (s *accessApplyStage) View() string     { return "" }

func (s *accessApplyStage) Enter(ctx context.Context) tea.Cmd {
	s.working = true
	w := s.w
	changes := w.changes()
	affected := w.affectedVMs()
	return busyThen(fmt.Sprintf("Updating the registry and %d VM(s) …", len(affected)), func() tea.Msg {
		acc, err := accessreg.UpdateAccess(ctx, w.env.fs, func(a *accessreg.Access) error {
			for _, c := range changes {
				if !c.after.AllVMs && len(c.after.VMs) == 0 {
					a.RevokeAll(c.name)
					continue
				}
				a.People[c.name] = c.after
			}
			return nil
		})
		if err != nil {
			return accessAppliedMsg{err: err}
		}
		return accessAppliedMsg{outcomes: syncVMs(ctx, w.env, acc, w.keys, affected)}
	})
}

func (s *accessApplyStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	m, ok := msg.(accessAppliedMsg)
	if !ok {
		return s, nil
	}
	s.working = false
	if m.err != nil {
		return s, wizard.Abort(m.err)
	}
	lines := []wizard.Line{{Text: "registry updated: " + accessreg.AccessFile}}
	for _, d := range s.w.diffLines() {
		lines = append(lines, wizard.Line{Text: "  " + d})
	}
	for _, o := range m.outcomes {
		mark := map[string]string{"updated": "✓", "unchanged": "=", "pending": "…", "failed": "✗"}[o.Status]
		lines = append(lines, wizard.Line{Warn: o.Status == "failed", Text: fmt.Sprintf("%s %s (%d): %s", mark, o.VM.Name, o.VM.VMID, o.Detail)})
	}
	return s, wizard.Finish(wizard.Result{Summary: lines})
}
