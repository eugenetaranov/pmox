package pveclient

import (
	"context"
)

type versionInfo struct {
	Version string `json:"version"`
	Release string `json:"release"`
	Repoid  string `json:"repoid"`
}

// GetVersion calls GET /version and returns the version string.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	v, err := getData[versionInfo](ctx, c, "/version", nil, "version response")
	if err != nil {
		return "", err
	}
	return v.Version, nil
}
