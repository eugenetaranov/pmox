package pveclient

import (
	"context"
	"net/url"
	"strings"
)

// VMState is a VM power state as reported by PVE ("running",
// "stopped", ...). The Status fields stay plain strings for backward
// compatibility; use State() / IsRunning() for typed access.
type VMState string

// Known VM states.
const (
	StateRunning VMState = "running"
	StateStopped VMState = "stopped"
)

// Resource is one row from GET /cluster/resources. Fields are copied
// verbatim from the PVE response — no normalization at the client
// layer so callers can distinguish "tags field absent" from "tag field
// set to empty". Integer fields are decoded leniently (numbers, quoted
// numbers or bools).
type Resource struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Status   string `json:"status"`
	Tags     string `json:"tags"`
	Uptime   int64  `json:"uptime"`   // seconds; free from the same response
	Template int    `json:"template"` // 1 if this VM is a template
}

// State returns the VM's status as a VMState.
func (r Resource) State() VMState { return VMState(r.Status) }

// IsRunning reports whether the VM's status is "running".
func (r Resource) IsRunning() bool { return r.State() == StateRunning }

// IsTemplate reports whether the VM is a template.
func (r Resource) IsTemplate() bool { return r.Template != 0 }

// TagList splits the raw PVE tags field (';' or ',' separated) into
// trimmed, non-empty tags.
func (r Resource) TagList() []string {
	fields := strings.FieldsFunc(r.Tags, func(c rune) bool { return c == ';' || c == ',' })
	out := fields[:0]
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// HasTag reports whether the VM carries tag name (case-insensitive),
// mirroring vm.HasPMOXTag's parsing.
func (r Resource) HasTag(name string) bool {
	for _, t := range r.TagList() {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// ClusterResources issues GET /cluster/resources, optionally filtered
// by resource type (e.g. "vm"). An empty typeFilter omits the query
// string entirely.
func (c *Client) ClusterResources(ctx context.Context, typeFilter string) ([]Resource, error) {
	var q url.Values
	if typeFilter != "" {
		q = url.Values{}
		q.Set("type", typeFilter)
	}
	return getData[[]Resource](ctx, c, "/cluster/resources", q, "cluster resources")
}
