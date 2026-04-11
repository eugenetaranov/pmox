package template

import (
	"context"
	"fmt"
	"strings"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// dirCapable reports whether a storage type can hold snippets — the
// five filesystem-backed types the PVE content list accepts.
func dirCapable(s pveclient.Storage) bool {
	switch s.Type {
	case "dir", "nfs", "cifs", "cephfs", "glusterfs":
		return true
	}
	return false
}

// hasContent returns true if the storage's content list includes the
// given entry (comma-separated, whitespace-tolerant).
func hasContent(s pveclient.Storage, content string) bool {
	for _, c := range strings.Split(s.Content, ",") {
		if strings.TrimSpace(c) == content {
			return true
		}
	}
	return false
}

// appendContent merges a new entry into an existing comma-separated
// content list without duplicating an existing entry.
func appendContent(existing, add string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return add
	}
	for _, c := range strings.Split(existing, ",") {
		if strings.TrimSpace(c) == add {
			return existing
		}
	}
	return existing + "," + add
}

// ensureSnippetsStorage runs the three-state logic from design D4:
//  1. If any storage already lists "snippets" in its content, return
//     the alphabetically-first such storage silently.
//  2. Otherwise, if a dir-capable storage exists without snippets,
//     prompt via confirm(name) and on yes issue UpdateStorageContent
//     with the existing list + "snippets" appended.
//  3. Otherwise, return a hard error instructing the user to create
//     a directory-type storage via the Proxmox UI.
func ensureSnippetsStorage(ctx context.Context, c *pveclient.Client, node string, confirm func(string) bool) (string, error) {
	pools, err := c.ListStorage(ctx, node)
	if err != nil {
		return "", fmt.Errorf("list storage: %w", err)
	}

	// State 1: already enabled somewhere. ListStorage already returns
	// pools sorted by name.
	for _, s := range pools {
		if hasContent(s, "snippets") {
			return s.Storage, nil
		}
	}

	// State 2: dir-capable without snippets — pick the first.
	var candidate *pveclient.Storage
	for i := range pools {
		if dirCapable(pools[i]) {
			candidate = &pools[i]
			break
		}
	}
	if candidate == nil {
		return "", fmt.Errorf("no dir-capable storage found; create a directory-type storage (dir/nfs/cifs/cephfs/glusterfs) in the Proxmox UI first")
	}

	if !confirm(candidate.Storage) {
		return "", fmt.Errorf("snippets storage required but user declined to enable snippets on %s", candidate.Storage)
	}

	newContent := appendContent(candidate.Content, "snippets")
	if err := c.UpdateStorageContent(ctx, candidate.Storage, newContent); err != nil {
		return "", fmt.Errorf("enable snippets on %s: %w", candidate.Storage, err)
	}
	return candidate.Storage, nil
}
