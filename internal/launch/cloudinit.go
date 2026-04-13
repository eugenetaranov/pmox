package launch

import (
	"fmt"
	"strconv"

	"github.com/eugenetaranov/pmox/internal/snippet"
)

// BuildCustomKV returns the SetConfig key/value map pmox pushes during
// phase 5. Every pmox-launched VM gets its user-data from a snippet on
// disk, so the map never contains ciuser, sshkeys, or cipassword — the
// uploaded file owns those. The cicustom value follows the frozen
// `pmox-<vmid>-user-data.yaml` convention on opts.Storage. An ide2
// cloud-init drive is still needed so PVE has somewhere to serialize
// the cicustom reference.
func BuildCustomKV(opts Options, vmid int) map[string]string {
	return map[string]string{
		"name":      opts.Name,
		"memory":    strconv.Itoa(opts.MemMB),
		"cores":     strconv.Itoa(opts.CPU),
		"agent":     "1",
		"ipconfig0": "ip=dhcp",
		"ide2":      fmt.Sprintf("%s:cloudinit", opts.Storage),
		"cicustom":  fmt.Sprintf("user=%s:snippets/%s", opts.Storage, snippet.Filename(vmid)),
	}
}
