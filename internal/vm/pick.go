package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

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
	selectOne   = tui.SelectOne
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

	if !isStdinTTY() || !isStderrTTY() {
		return nil, ErrPickerNonTTY
	}

	sort.Slice(pmoxVMs, func(i, j int) bool {
		return pmoxVMs[i].VMID < pmoxVMs[j].VMID
	})

	opts := make([]huh.Option[string], 0, len(pmoxVMs))
	byVMID := make(map[string]pveclient.Resource, len(pmoxVMs))
	for _, r := range pmoxVMs {
		label := formatPickerRow(r)
		key := fmt.Sprintf("%d", r.VMID)
		opts = append(opts, huh.NewOption(label, key))
		byVMID[key] = r
	}

	chosen := selectOne("Select a pmox VM", opts, "")
	if chosen == "" {
		// User aborted — SelectOne re-raised SIGINT, the context will be
		// cancelled shortly. Return an explicit error so the caller
		// doesn't proceed.
		return nil, fmt.Errorf("target selection cancelled")
	}
	r, ok := byVMID[chosen]
	if !ok {
		return nil, fmt.Errorf("picker returned unknown VMID %q", chosen)
	}
	return refFrom(r), nil
}

func formatPickerRow(r pveclient.Resource) string {
	return fmt.Sprintf("%s (%d, %s, %s)", r.Name, r.VMID, r.Node, r.Status)
}
