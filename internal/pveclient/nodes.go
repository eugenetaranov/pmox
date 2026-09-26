package pveclient

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Node represents a cluster node entry from GET /nodes.
type Node struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}

// Template represents a qemu VM template (from GET /nodes/{node}/qemu, filtered to template=1).
type Template struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
}

// Storage represents a storage pool entry. Active/Enabled are decoded
// leniently (numbers, quoted numbers or bools). Avail/Total (bytes) are
// PVE's live capacity for this storage — 0 when PVE didn't report it
// (e.g. the storage is inactive), never a value to treat as real free
// space.
type Storage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Active  int    `json:"active"`
	Enabled int    `json:"enabled"`
	Avail   int64  `json:"avail"`
	Total   int64  `json:"total"`
}

// HasContent reports whether the storage's comma-separated content list
// includes kind (e.g. "images", "snippets", "iso", "import").
func (s Storage) HasContent(kind string) bool {
	for _, c := range strings.Split(s.Content, ",") {
		if strings.TrimSpace(c) == kind {
			return true
		}
	}
	return false
}

// SupportsVMDisks reports whether the storage can hold VM disk images
// (i.e. its content list includes "images").
func (s Storage) SupportsVMDisks() bool { return s.HasContent("images") }

// SupportsSnippets reports whether the storage can hold cloud-init
// snippets (i.e. its content list includes "snippets").
func (s Storage) SupportsSnippets() bool { return s.HasContent("snippets") }

// ContentList returns the storage's content types as a slice, trimmed
// and with empty entries dropped. An empty content string yields nil.
func (s Storage) ContentList() []string {
	if strings.TrimSpace(s.Content) == "" {
		return nil
	}
	parts := strings.Split(s.Content, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FilterStorage returns the pools for which keep reports true, in order.
// Pass a method expression such as Storage.SupportsSnippets.
func FilterStorage(pools []Storage, keep func(Storage) bool) []Storage {
	var out []Storage
	for _, s := range pools {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// Bridge represents a network bridge entry.
type Bridge struct {
	Iface string `json:"iface"`
	Type  string `json:"type"`
}

// ListNodes fetches the list of cluster nodes.
func (c *Client) ListNodes(ctx context.Context) ([]Node, error) {
	nodes, err := getData[[]Node](ctx, c, "/nodes", nil, "nodes response")
	if err != nil {
		return nil, err
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Node < nodes[j].Node })
	return nodes, nil
}

// ListTemplates fetches VM templates on the given node.
// It calls GET /nodes/{node}/qemu and returns entries where template=1.
// Also returns the total number of VMs visible to the token, so callers can
// distinguish "no templates" from "no VM.Audit permission".
func (c *Client) ListTemplates(ctx context.Context, node string) ([]Template, int, error) {
	vms, err := getData[[]struct {
		VMID     json.Number `json:"vmid"`
		Name     string      `json:"name"`
		Template json.Number `json:"template"`
	}](ctx, c, "/nodes/"+url.PathEscape(node)+"/qemu", nil, "templates response")
	if err != nil {
		return nil, 0, err
	}
	out := make([]Template, 0, len(vms))
	for _, v := range vms {
		// PVE returns template as 0/1; some API versions/clients stringify it.
		// Treat anything non-zero and non-empty as "is a template".
		t := strings.TrimSpace(string(v.Template))
		if t == "" || t == "0" {
			continue
		}
		id, err := strconv.Atoi(string(v.VMID))
		if err != nil {
			continue
		}
		out = append(out, Template{VMID: id, Name: v.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VMID < out[j].VMID })
	return out, len(vms), nil
}

// ListStorage fetches storage pools on the given node.
func (c *Client) ListStorage(ctx context.Context, node string) ([]Storage, error) {
	pools, err := getData[[]Storage](ctx, c, "/nodes/"+url.PathEscape(node)+"/storage", nil, "storage response")
	if err != nil {
		return nil, err
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Storage < pools[j].Storage })
	return pools, nil
}

// ListBridges fetches network bridges on the given node.
func (c *Client) ListBridges(ctx context.Context, node string) ([]Bridge, error) {
	q := url.Values{}
	q.Set("type", "bridge")
	bridges, err := getData[[]Bridge](ctx, c, "/nodes/"+url.PathEscape(node)+"/network", q, "bridges response")
	if err != nil {
		return nil, err
	}
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].Iface < bridges[j].Iface })
	return bridges, nil
}
