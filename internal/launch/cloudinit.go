package launch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/eugenetaranov/pmox/internal/snippet"
)

// BuildCustomKV returns the SetConfig key/value map pmox pushes during
// phase 5. Every pmox-launched VM gets its user-data from a snippet on
// disk, so the map never contains ciuser, sshkeys, or cipassword — the
// uploaded file owns those. The cicustom value follows the frozen
// `pmox-<vmid>-user-data.yaml` convention on opts.SnippetStorage; the
// ide2 cloud-init drive lives on the VM disk storage (opts.Storage),
// since it has to share a backend with scsi0.
func BuildCustomKV(opts Options, vmid int) map[string]string {
	return map[string]string{
		"name":      opts.Name,
		"memory":    strconv.Itoa(opts.MemMB),
		"cores":     strconv.Itoa(opts.CPU),
		"agent":     "1",
		"ipconfig0": "ip=dhcp",
		"ide2":      fmt.Sprintf("%s:cloudinit", opts.Storage),
		"cicustom":  fmt.Sprintf("user=%s:snippets/%s", opts.SnippetStorage, snippet.Filename(vmid)),
	}
}

// setNet0Bridge returns the PVE net0 property string with its bridge=
// parameter set to bridge. net0 looks like
// "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1": the leading
// model[=MAC] and every other parameter are preserved in order. A
// missing bridge= is inserted right after the model; an empty net0
// yields a fresh "virtio,bridge=<bridge>" NIC.
func setNet0Bridge(net0, bridge string) string {
	param := "bridge=" + bridge
	if strings.TrimSpace(net0) == "" {
		return "virtio," + param
	}
	parts := strings.Split(net0, ",")
	for i, p := range parts {
		if strings.HasPrefix(p, "bridge=") {
			parts[i] = param
			return strings.Join(parts, ",")
		}
	}
	out := make([]string, 0, len(parts)+1)
	out = append(out, parts[0], param)
	out = append(out, parts[1:]...)
	return strings.Join(out, ",")
}
