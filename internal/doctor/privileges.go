package doctor

import "github.com/eugenetaranov/pmox/internal/pveclient"

// RequiredPriv is one privilege pmox needs, with the path it applies to
// and a short reason (mirrors the table in docs/pve-setup.md).
type RequiredPriv struct {
	Priv string
	Path string
	Why  string
}

// RequiredPrivileges returns the privilege set pmox needs for a full
// launch/create-template flow, scoped to the configured disk and snippet
// storage pools. diskStorage and snippetStorage may be empty, in which
// case their pool-scoped entries are omitted.
func RequiredPrivileges(diskStorage, snippetStorage string) []RequiredPriv {
	reqs := []RequiredPriv{
		{"Sys.Audit", "/", "list nodes and read network bridges"},
		{"VM.Audit", "/vms", "list VMs and discover templates"},
		{"VM.Allocate", "/vms", "create new VMs"},
		{"VM.Clone", "/vms", "clone a template into a new VM"},
		{"VM.Config.Disk", "/vms", "set the VM disk"},
		{"VM.Config.CPU", "/vms", "set cores"},
		{"VM.Config.Memory", "/vms", "set memory"},
		{"VM.Config.Network", "/vms", "attach the NIC"},
		{"VM.Config.Cloudinit", "/vms", "push cloud-init config"},
		{"VM.PowerMgmt", "/vms", "start and stop VMs"},
		{"Datastore.Audit", "/storage", "list storage pools"},
		{"SDN.Use", "/sdn/zones/localnetwork", "attach NICs to bridges"},
	}
	if diskStorage != "" {
		reqs = append(reqs, RequiredPriv{"Datastore.AllocateSpace", "/storage/" + diskStorage, "allocate the VM disk on " + diskStorage})
	}
	if snippetStorage != "" {
		reqs = append(reqs, RequiredPriv{"Datastore.AllocateSpace", "/storage/" + snippetStorage, "write the cloud-init snippet on " + snippetStorage})
	}
	return reqs
}

// MissingPrivileges returns the required privileges not granted by perms
// (accounting for inheritance from ancestor paths).
func MissingPrivileges(perms pveclient.Permissions, required []RequiredPriv) []RequiredPriv {
	var missing []RequiredPriv
	for _, req := range required {
		if !perms.HasPriv(req.Path, req.Priv) {
			missing = append(missing, req)
		}
	}
	return missing
}
