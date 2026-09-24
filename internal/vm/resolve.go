// Package vm holds CLI-side helpers shared across the VM lifecycle
// commands (delete today; list, info, start, stop, clone later). It
// depends on pveclient but not on cobra — cmd/pmox wires it in.
package vm

import (
	"context"
	"errors"
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
		return nil, notFound(fmt.Sprintf("VM %d not found", n))
	}

	return resolveByName(resources, arg)
}

// resolveByName matches arg against VM names the same way `pmox list`
// discovers VMs by default: pmox-tagged VMs are preferred. Otherwise an
// untagged VM that shares a name with a tagged one — invisible in
// `pmox list`'s default view — would make the tagged VM's own name
// "ambiguous", which is confusing since the user cannot see why.
// Untagged VMs are only matched when no tagged VM has that name, so a
// name that is unique among untagged VMs still resolves.
func resolveByName(resources []pveclient.Resource, arg string) (*Ref, error) {
	var tagged, all []pveclient.Resource
	for _, r := range resources {
		if r.Name != arg {
			continue
		}
		all = append(all, r)
		if HasPMOXTag(r.Tags) {
			tagged = append(tagged, r)
		}
	}
	matches := tagged
	if len(matches) == 0 {
		matches = all
	}
	switch len(matches) {
	case 0:
		return nil, notFound(fmt.Sprintf("VM %q not found", arg))
	case 1:
		return refFrom(matches[0]), nil
	default:
		vmids := make([]int, len(matches))
		for i, m := range matches {
			vmids[i] = m.VMID
		}
		sort.Ints(vmids)
		return nil, &sentinelError{
			msg:      fmt.Sprintf("multiple VMs named %q: vmids %v — pass the VMID instead", arg, vmids),
			sentinel: ErrAmbiguous,
		}
	}
}

// ErrAmbiguous is wrapped by Resolve when a name matches more than one
// VM. Resolve's not-found errors wrap pveclient.ErrNotFound.
var ErrAmbiguous = errors.New("ambiguous VM name")

// sentinelError keeps a human-facing message while letting errors.Is
// match a sentinel that the message doesn't repeat.
type sentinelError struct {
	msg      string
	sentinel error
}

func (e *sentinelError) Error() string { return e.msg }
func (e *sentinelError) Unwrap() error { return e.sentinel }

func notFound(msg string) error {
	return &sentinelError{msg: msg, sentinel: pveclient.ErrNotFound}
}

// RequirePMOXTag returns an error unless r carries the `pmox` tag or
// force is set. verb completes "refusing to <verb> VM ...", e.g.
// "delete" or "connect to".
func (r *Ref) RequirePMOXTag(verb string, force bool) error {
	if force || HasPMOXTag(r.Tags) {
		return nil
	}
	return fmt.Errorf("refusing to %s VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", verb, r.Name, r.VMID)
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
