package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
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
//
// stderr is used for informational output (e.g. picker-adjacent status);
// pass cmd.ErrOrStderr() from a cobra handler.
func Pick(ctx context.Context, client *pveclient.Client, _ io.Writer) (*Ref, error) {
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}

	var pmoxVMs []pveclient.Resource
	for _, r := range resources {
		if HasPMOXTag(r.Tags) {
			pmoxVMs = append(pmoxVMs, r)
		}
	}

	switch len(pmoxVMs) {
	case 0:
		return nil, ErrNoPMOXVMs
	case 1:
		return refFrom(pmoxVMs[0]), nil
	}

	sortPickerVMs(pmoxVMs)

	if !isStdinTTY() || !isStderrTTY() || noInput() {
		// Can't draw a picker — tell the caller what the valid choices
		// are (like the server resolver does) so they don't have to run
		// `pmox list` to find a name.
		return nil, fmt.Errorf("%w\navailable VMs:\n%s", ErrPickerNonTTY, candidateList(pmoxVMs))
	}

	opts := make([]huh.Option[string], 0, len(pmoxVMs))
	byVMID := make(map[string]pveclient.Resource, len(pmoxVMs))
	for _, r := range pmoxVMs {
		key := fmt.Sprintf("%d", r.VMID)
		opts = append(opts, huh.NewOption(formatPickerRow(r), key))
		byVMID[key] = r
	}

	chosen, err := selectOne("Select a pmox VM", opts)
	if err != nil {
		return nil, err
	}
	r, ok := byVMID[chosen]
	if !ok {
		return nil, fmt.Errorf("picker returned unknown VMID %q", chosen)
	}
	return refFrom(r), nil
}

// PickMulti returns one or more pmox-tagged VMs. With exactly one such
// VM it is returned without prompting; with several, a multi-select
// picker (space toggles, enter confirms) is shown on a TTY. Zero VMs →
// ErrNoPMOXVMs; non-interactive with several → ErrPickerNonTTY listing
// the candidates.
func PickMulti(ctx context.Context, client *pveclient.Client, _ io.Writer) ([]*Ref, error) {
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}
	var pmoxVMs []pveclient.Resource
	for _, r := range resources {
		if HasPMOXTag(r.Tags) {
			pmoxVMs = append(pmoxVMs, r)
		}
	}

	switch len(pmoxVMs) {
	case 0:
		return nil, ErrNoPMOXVMs
	case 1:
		return []*Ref{refFrom(pmoxVMs[0])}, nil
	}

	sortPickerVMs(pmoxVMs)

	if !isStdinTTY() || !isStderrTTY() || noInput() {
		return nil, fmt.Errorf("%w\navailable VMs:\n%s", ErrPickerNonTTY, candidateList(pmoxVMs))
	}

	opts := make([]huh.Option[string], 0, len(pmoxVMs))
	byVMID := make(map[string]pveclient.Resource, len(pmoxVMs))
	for _, r := range pmoxVMs {
		key := fmt.Sprintf("%d", r.VMID)
		opts = append(opts, huh.NewOption(formatPickerRow(r), key))
		byVMID[key] = r
	}

	chosen, err := selectMulti("Select pmox VMs (space to toggle, enter to confirm)", opts)
	if err != nil {
		return nil, err
	}
	refs := make([]*Ref, 0, len(chosen))
	for _, key := range chosen {
		r, ok := byVMID[key]
		if !ok {
			return nil, fmt.Errorf("picker returned unknown VMID %q", key)
		}
		refs = append(refs, refFrom(r))
	}
	return refs, nil
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
