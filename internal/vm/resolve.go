// Package vm holds CLI-side helpers shared across the VM lifecycle
// commands (delete today; list, info, start, stop, clone later). It
// depends on pveclient but not on cobra — cmd/pmox wires it in.
package vm

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Ref is a resolved reference to a single VM on the cluster.
type Ref struct {
	VMID int
	Node string
	Name string
	Tags string
}

// Resolve turns a user-supplied name-or-VMID into a Ref via a single
// /cluster/resources call. A purely numeric arg is treated as a VMID;
// anything else is matched against names.
func Resolve(ctx context.Context, c *pveclient.Client, arg string) (*Ref, error) {
	resources, err := c.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}

	if n, err := strconv.Atoi(arg); err == nil {
		for _, r := range resources {
			if r.VMID == n {
				return refFrom(r), nil
			}
		}
		return nil, fmt.Errorf("VM %d not found", n)
	}

	var matches []pveclient.Resource
	for _, r := range resources {
		if r.Name == arg {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("VM %q not found", arg)
	case 1:
		return refFrom(matches[0]), nil
	default:
		vmids := make([]int, len(matches))
		for i, m := range matches {
			vmids[i] = m.VMID
		}
		sort.Ints(vmids)
		return nil, fmt.Errorf("multiple VMs named %q: vmids %v — pass the VMID instead", arg, vmids)
	}
}

func refFrom(r pveclient.Resource) *Ref {
	return &Ref{VMID: r.VMID, Node: r.Node, Name: r.Name, Tags: r.Tags}
}

// HasPMOXTag reports whether the PVE tags field contains the literal
// `pmox` tag. PVE has shipped both `;` and `,` as the tag separator
// across versions, so both are accepted. Matching is case-insensitive
// and substring matches (e.g. `pmoxish`) do not count.
func HasPMOXTag(tagsRaw string) bool {
	fields := strings.FieldsFunc(tagsRaw, func(r rune) bool {
		return r == ';' || r == ','
	})
	for _, f := range fields {
		if strings.EqualFold(strings.TrimSpace(f), "pmox") {
			return true
		}
	}
	return false
}
