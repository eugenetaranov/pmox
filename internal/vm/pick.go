package vm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// ErrNoPMOXVMs is returned by Pick when the cluster has zero pmox-tagged
// VMs. Callers can surface this as a friendly "run pmox launch" message.
var ErrNoPMOXVMs = errors.New("no pmox VMs found on the cluster — run `pmox launch` to create one")

// ErrPickerNonTTY is returned by Pick when more than one pmox VM exists
// but stdin or stderr is not a terminal, so no picker can be drawn.
// The message matches the "missing argument" shape scripts see today.
var ErrPickerNonTTY = errors.New("missing VM argument — pass a <name|vmid> positional or run in a terminal for the interactive picker")

// Picker injection points — tests override these to bypass the real TUI.
var (
	isStdinTTY  = tui.StdinIsTerminal
	isStderrTTY = tui.StderrIsTerminal
	noInput     = tui.NoInput
	selectOne   = tui.Select
	selectMulti = tui.SelectMulti
)

// Pick returns a single pmox-tagged VM. When the cluster has exactly one
// such VM, it is returned silently. When multiple exist and both stdin
// and stderr are terminals, an interactive picker is shown. When zero
// exist, ErrNoPMOXVMs is returned. When multiple exist but the session
// is non-interactive, ErrPickerNonTTY is returned.
func Pick(ctx context.Context, client *pveclient.Client) (*Ref, error) {
	single, c, err := loadPickerCandidates(ctx, client)
	if err != nil || single != nil {
		return single, err
	}
	chosen, err := selectOne("Select a pmox VM", c.opts)
	if err != nil {
		return nil, err
	}
	refs, err := c.refs([]string{chosen})
	if err != nil {
		return nil, err
	}
	return refs[0], nil
}

// PickMulti returns one or more pmox-tagged VMs. With exactly one such
// VM it is returned without prompting; with several, a multi-select
// picker (space toggles, enter confirms) is shown on a TTY. Zero VMs →
// ErrNoPMOXVMs; non-interactive with several → ErrPickerNonTTY listing
// the candidates.
func PickMulti(ctx context.Context, client *pveclient.Client) ([]*Ref, error) {
	single, c, err := loadPickerCandidates(ctx, client)
	if err != nil {
		return nil, err
	}
	if single != nil {
		return []*Ref{single}, nil
	}
	chosen, err := selectMulti("Select pmox VMs (space to toggle, enter to confirm)", c.opts)
	if err != nil {
		return nil, err
	}
	return c.refs(chosen)
}

// pickerCandidates holds the picker options for two or more pmox VMs,
// keyed by stringified VMID.
type pickerCandidates struct {
	opts   []huh.Option[string]
	byVMID map[string]pveclient.Resource
}

// loadPickerCandidates lists pmox-tagged VMs. With exactly one it is
// returned as single (no prompt needed). With several it returns the
// sorted picker candidates, or ErrPickerNonTTY when no picker can be
// drawn. Zero VMs → ErrNoPMOXVMs.
func loadPickerCandidates(ctx context.Context, client *pveclient.Client) (single *Ref, c *pickerCandidates, err error) {
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, nil, fmt.Errorf("list cluster resources: %w", err)
	}

	var pmoxVMs []pveclient.Resource
	for _, r := range resources {
		if HasPMOXTag(r.Tags) {
			pmoxVMs = append(pmoxVMs, r)
		}
	}

	switch len(pmoxVMs) {
	case 0:
		return nil, nil, ErrNoPMOXVMs
	case 1:
		return refFrom(pmoxVMs[0]), nil, nil
	}

	sortPickerVMs(pmoxVMs)

	if !isStdinTTY() || !isStderrTTY() || noInput() {
		// Can't draw a picker — tell the caller what the valid choices
		// are (like the server resolver does) so they don't have to run
		// `pmox list` to find a name.
		return nil, nil, fmt.Errorf("%w\navailable VMs:\n%s", ErrPickerNonTTY, candidateList(pmoxVMs))
	}

	c = &pickerCandidates{
		opts:   make([]huh.Option[string], 0, len(pmoxVMs)),
		byVMID: make(map[string]pveclient.Resource, len(pmoxVMs)),
	}
	for _, r := range pmoxVMs {
		key := fmt.Sprintf("%d", r.VMID)
		c.opts = append(c.opts, huh.NewOption(formatPickerRow(r), key))
		c.byVMID[key] = r
	}
	return nil, c, nil
}

// refs maps picker keys back to Refs, in the order given.
func (c *pickerCandidates) refs(keys []string) ([]*Ref, error) {
	out := make([]*Ref, 0, len(keys))
	for _, key := range keys {
		r, ok := c.byVMID[key]
		if !ok {
			return nil, fmt.Errorf("picker returned unknown VMID %q", key)
		}
		out = append(out, refFrom(r))
	}
	return out, nil
}

// sortPickerVMs orders running VMs first, then by VMID — so live targets
// aren't buried behind stopped ones in connect-oriented pickers.
func sortPickerVMs(vms []pveclient.Resource) {
	sort.Slice(vms, func(i, j int) bool {
		ri, rj := vms[i].Status == "running", vms[j].Status == "running"
		if ri != rj {
			return ri
		}
		return vms[i].VMID < vms[j].VMID
	})
}

func candidateList(vms []pveclient.Resource) string {
	lines := make([]string, 0, len(vms))
	for _, r := range vms {
		lines = append(lines, "  - "+formatPickerRow(r))
	}
	return strings.Join(lines, "\n")
}

func formatPickerRow(r pveclient.Resource) string {
	if r.Status == "running" && r.Uptime > 0 {
		return fmt.Sprintf("%s (%d, %s, %s, up %s)", r.Name, r.VMID, r.Node, r.Status, shortDuration(r.Uptime))
	}
	return fmt.Sprintf("%s (%d, %s, %s)", r.Name, r.VMID, r.Node, r.Status)
}

// shortDuration renders an uptime in seconds as a compact "3d"/"5h"/"12m".
func shortDuration(secs int64) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
