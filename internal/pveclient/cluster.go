package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Resource is one row from GET /cluster/resources. Fields are copied
// verbatim from the PVE response — no normalization at the client
// layer so callers can distinguish "tags field absent" from "tag field
// set to empty".
type Resource struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Status string `json:"status"`
	Tags   string `json:"tags"`
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
	body, err := c.request(ctx, "GET", "/cluster/resources", q)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []Resource `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse cluster resources: %w", err)
	}
	return payload.Data, nil
}
