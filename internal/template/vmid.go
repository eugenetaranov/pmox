package template

import (
	"context"
	"fmt"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

const (
	vmidRangeLo = 9000
	vmidRangeHi = 9099
)

// reserveVMID scans every VM visible to the token via
// /cluster/resources, filters to the 9000–9099 template range, and
// returns the lowest unused slot. Errors if the range is fully
// occupied — the user needs to clean up orphaned build VMs.
//
// ClusterResources is used instead of ListTemplates because we need
// to avoid *any* VMID in the range, not just ones already converted
// to templates. A half-built VM (status=stopped, template=0) must
// still reserve its slot.
func reserveVMID(ctx context.Context, c *pveclient.Client, node string) (int, error) {
	resources, err := c.ClusterResources(ctx, "vm")
	if err != nil {
		return 0, fmt.Errorf("list cluster vms: %w", err)
	}
	used := make(map[int]bool)
	for _, r := range resources {
		if r.VMID >= vmidRangeLo && r.VMID <= vmidRangeHi {
			used[r.VMID] = true
		}
	}
	for id := vmidRangeLo; id <= vmidRangeHi; id++ {
		if !used[id] {
			return id, nil
		}
	}
	return 0, fmt.Errorf("vmid range %d-%d is full; delete unused templates first", vmidRangeLo, vmidRangeHi)
}
