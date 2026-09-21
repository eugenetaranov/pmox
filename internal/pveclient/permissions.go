package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Permissions is the token's effective ACL, keyed by path, each mapping
// a privilege name to whether it is granted. PVE resolves inheritance
// per listed path, but a privilege granted on an ancestor path (e.g. "/"
// with propagate) may only appear under that ancestor — HasPriv walks
// ancestors to account for this.
type Permissions map[string]map[string]bool

// GetPermissions calls GET /access/permissions and returns the caller's
// resolved permission set. This is a read-only introspection endpoint —
// safe to call from diagnostics.
func (c *Client) GetPermissions(ctx context.Context) (Permissions, error) {
	body, err := c.request(ctx, "GET", "/access/permissions", nil)
	if err != nil {
		return nil, err
	}
	// PVE returns {"data": {"/path": {"Priv.Name": 1, ...}, ...}}.
	var resp struct {
		Data map[string]map[string]int `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse permissions response: %w", err)
	}
	perms := make(Permissions, len(resp.Data))
	for path, privs := range resp.Data {
		set := make(map[string]bool, len(privs))
		for name, v := range privs {
			set[name] = v != 0
		}
		perms[path] = set
	}
	return perms, nil
}

// HasPriv reports whether priv is granted at path, accounting for
// inheritance: a privilege present on any ancestor path (walking up to
// "/") counts as granted at the descendant path.
func (p Permissions) HasPriv(path, priv string) bool {
	for _, ancestor := range ancestorPaths(path) {
		if set, ok := p[ancestor]; ok && set[priv] {
			return true
		}
	}
	return false
}

// ancestorPaths returns path and each of its ancestors up to "/".
// e.g. "/storage/local" -> ["/storage/local", "/storage", "/"].
func ancestorPaths(path string) []string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return []string{"/"}
	}
	out := []string{path}
	for {
		i := strings.LastIndex(path, "/")
		if i <= 0 {
			break
		}
		path = path[:i]
		out = append(out, path)
	}
	out = append(out, "/")
	return out
}
