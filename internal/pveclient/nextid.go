package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// NextID returns the next available VMID reported by
// GET /cluster/nextid. The PVE API returns the value as a string
// (e.g. {"data":"100"}), which this method parses to int for callers.
func (c *Client) NextID(ctx context.Context) (int, error) {
	body, err := c.request(ctx, "GET", "/cluster/nextid", nil)
	if err != nil {
		return 0, err
	}
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("parse nextid response: %w", err)
	}
	n, err := strconv.Atoi(payload.Data)
	if err != nil {
		return 0, fmt.Errorf("parse nextid response %q: %w", payload.Data, err)
	}
	return n, nil
}
