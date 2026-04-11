package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
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

// Storage represents a storage pool entry.
type Storage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Active  int    `json:"active"`
	Enabled int    `json:"enabled"`
}

// SupportsVMDisks reports whether the storage can hold VM disk images
// (i.e. its content list includes "images").
func (s Storage) SupportsVMDisks() bool {
	for _, c := range strings.Split(s.Content, ",") {
		if strings.TrimSpace(c) == "images" {
			return true
		}
	}
	return false
}

// Bridge represents a network bridge entry.
type Bridge struct {
	Iface string `json:"iface"`
	Type  string `json:"type"`
}

// ListNodes fetches the list of cluster nodes.
func (c *Client) ListNodes(ctx context.Context) ([]Node, error) {
	body, err := c.request(ctx, "GET", "/nodes", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []Node `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse nodes response: %w", err)
	}
	sort.Slice(resp.Data, func(i, j int) bool { return resp.Data[i].Node < resp.Data[j].Node })
	return resp.Data, nil
}

// ListTemplates fetches VM templates on the given node.
// It calls GET /nodes/{node}/qemu and returns entries where template=1.
// Also returns the total number of VMs visible to the token, so callers can
// distinguish "no templates" from "no VM.Audit permission".
func (c *Client) ListTemplates(ctx context.Context, node string) ([]Template, int, error) {
	body, err := c.request(ctx, "GET", "/nodes/"+node+"/qemu", nil)
	if err != nil {
		return nil, 0, err
	}
	var resp struct {
		Data []struct {
			VMID     json.Number `json:"vmid"`
			Name     string      `json:"name"`
			Template json.Number `json:"template"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("parse templates response: %w", err)
	}
	out := make([]Template, 0, len(resp.Data))
	for _, v := range resp.Data {
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
	return out, len(resp.Data), nil
}

// ListStorage fetches storage pools on the given node.
func (c *Client) ListStorage(ctx context.Context, node string) ([]Storage, error) {
	body, err := c.request(ctx, "GET", "/nodes/"+node+"/storage", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []Storage `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse storage response: %w", err)
	}
	sort.Slice(resp.Data, func(i, j int) bool { return resp.Data[i].Storage < resp.Data[j].Storage })
	return resp.Data, nil
}

// ListBridges fetches network bridges on the given node.
func (c *Client) ListBridges(ctx context.Context, node string) ([]Bridge, error) {
	q := url.Values{}
	q.Set("type", "bridge")
	body, err := c.request(ctx, "GET", "/nodes/"+node+"/network", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []Bridge `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse bridges response: %w", err)
	}
	sort.Slice(resp.Data, func(i, j int) bool { return resp.Data[i].Iface < resp.Data[j].Iface })
	return resp.Data, nil
}
