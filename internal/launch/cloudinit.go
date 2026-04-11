package launch

import (
	"strconv"
	"strings"
)

// BuildBuiltinKV returns the SetConfig key/value map pmox pushes
// during phase 5 when `--cloud-init` is not supplied. The map covers
// resource knobs (memory, cores, name), agent enable, and the
// built-in cloud-init keys (ciuser, sshkeys, ipconfig0). It does NOT
// contain a `cicustom` key — snippets-mode cloud-init is slice 7's
// territory, per design D4.
//
// The `sshkeys` value is the raw public key string; `pveclient.SetConfig`
// handles the PVE-specific double URL-encoding quirk.
func BuildBuiltinKV(opts Options, vmid int) map[string]string {
	_ = vmid // reserved for future per-vmid customization
	return map[string]string{
		"name":      opts.Name,
		"memory":    strconv.Itoa(opts.MemMB),
		"cores":     strconv.Itoa(opts.CPU),
		"agent":     "1",
		"ciuser":    opts.User,
		"sshkeys":   strings.TrimSpace(opts.SSHPubKey),
		"ipconfig0": "ip=dhcp",
	}
}
