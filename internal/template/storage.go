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

// pickTargetStorage lists storage pools on the node, filters to
// images-capable ones, and delegates to opts.PickTargetStorage for the
// user choice.
func pickTargetStorage(ctx context.Context, opts Options) (string, error) {
	return pickStorage(ctx, opts, storagePick{
		what:  "target storage",
		step:  "Listing images-capable storage for the template disk",
		keep:  pveclient.Storage.SupportsVMDisks,
		none:  "no active, enabled, images-capable storage",
		chose: opts.PickTargetStorage,
	})
}

// pickSnippetsStorage lists storage pools on the node, filters to
// dir-capable ones, and delegates to opts.PickSnippetsStorage for the
// user choice. Unlike the old ensureSnippetsStorage flow, this does
// NOT mutate the pool's content list — pmox writes the snippet file
// directly via SFTP, so the PVE `content=` whitelist is irrelevant.
func pickSnippetsStorage(ctx context.Context, opts Options) (string, error) {
	return pickStorage(ctx, opts, storagePick{
		what:  "snippets storage",
		step:  "Listing dir-capable storage for snippets",
		keep:  dirCapable,
		none:  "no dir-capable storage (dir/nfs/cifs/cephfs/glusterfs)",
		chose: opts.PickSnippetsStorage,
	})
}

// storagePick parameterizes pickStorage for one storage phase.
type storagePick struct {
	what  string // error prefix: "pick <what>: ..."
	step  string // progress label for the ListStorage call
	keep  func(pveclient.Storage) bool
	none  string // error text when no pool survives the filter
	chose func([]pveclient.Storage) int
}

// pickStorage lists storage on opts.Node, keeps active+enabled pools
// that satisfy p.keep, and asks p.chose to pick one.
func pickStorage(ctx context.Context, opts Options, p storagePick) (string, error) {
	opts.pStart(p.step)
	pools, err := opts.Client.ListStorage(ctx, opts.Node)
	opts.pDone(err)
	if err != nil {
		return "", fmt.Errorf("pick %s: %w", p.what, err)
	}
	usable := make([]pveclient.Storage, 0, len(pools))
	for _, s := range pools {
		if s.Active == 1 && s.Enabled == 1 && p.keep(s) {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		return "", fmt.Errorf("pick %s: %s found on node %s", p.what, p.none, opts.Node)
	}
	if p.chose == nil {
		return "", fmt.Errorf("pick %s: no picker supplied", p.what)
	}
	idx := p.chose(usable)
	if idx < 0 || idx >= len(usable) {
		return "", fmt.Errorf("pick %s: picker returned out-of-range index %d", p.what, idx)
	}
	return usable[idx].Storage, nil
}
