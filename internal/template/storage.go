package template

import (
	"context"
	"fmt"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// dirCapable reports whether a storage type can hold snippets — the
// five filesystem-backed types PVE recognises.
func dirCapable(s pveclient.Storage) bool {
	switch s.Type {
	case "dir", "nfs", "cifs", "cephfs", "glusterfs":
		return true
	}
	return false
}

// pickSnippetsStorage lists storage pools on the node, filters to
// dir-capable ones, and delegates to opts.PickSnippetsStorage for the
// user choice. Unlike the old ensureSnippetsStorage flow, this does
// NOT mutate the pool's content list — pmox writes the snippet file
// directly via SFTP, so the PVE `content=` whitelist is irrelevant.
func pickSnippetsStorage(ctx context.Context, opts Options) (string, error) {
	pools, err := opts.Client.ListStorage(ctx, opts.Node)
	if err != nil {
		return "", fmt.Errorf("pick snippets storage: %w", err)
	}
	usable := make([]pveclient.Storage, 0, len(pools))
	for _, s := range pools {
		if s.Active == 1 && s.Enabled == 1 && dirCapable(s) {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		return "", fmt.Errorf("pick snippets storage: no dir-capable storage (dir/nfs/cifs/cephfs/glusterfs) found on node %s", opts.Node)
	}
	if opts.PickSnippetsStorage == nil {
		return "", fmt.Errorf("pick snippets storage: no picker supplied")
	}
	idx := opts.PickSnippetsStorage(usable)
	if idx < 0 || idx >= len(usable) {
		return "", fmt.Errorf("pick snippets storage: picker returned out-of-range index %d", idx)
	}
	return usable[idx].Storage, nil
}
