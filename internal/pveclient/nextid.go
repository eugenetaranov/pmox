package pveclient

import (
	"context"
	"fmt"
	"strconv"
)

// NextID returns the next available VMID reported by
// GET /cluster/nextid. The PVE API returns the value as a string
// (e.g. {"data":"100"}), which this method parses to int for callers.
func (c *Client) NextID(ctx context.Context) (int, error) {
	data, err := getData[string](ctx, c, "/cluster/nextid", nil, "nextid response")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(data)
	if err != nil {
		return 0, fmt.Errorf("parse nextid response %q: %w", data, err)
	}
	return n, nil
}
