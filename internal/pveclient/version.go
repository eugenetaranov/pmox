package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
)

type versionResponse struct {
	Data struct {
		Version string `json:"version"`
		Release string `json:"release"`
		Repoid  string `json:"repoid"`
	} `json:"data"`
}

// GetVersion calls GET /version and returns the version string.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	body, err := c.request(ctx, "GET", "/version", nil)
	if err != nil {
		return "", err
	}
	var v versionResponse
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("parse version response: %w", err)
	}
	return v.Data.Version, nil
}
