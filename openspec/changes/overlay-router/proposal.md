# Proposal

## Why

Away from home, pmox can reach the Proxmox API and node SSH only if something on the home LAN puts them on the overlay network. Today the user sets that up by hand: Tailscale on the host itself, or a container they build. pmox already holds the API token and node SSH, so it can create and manage a small router container on the bridge. That reaches the node without touching the hypervisor's packages.

A panel review (Tailscale, Proxmox admin, security, pmox maintainer) settled the shape:
- an unprivileged LXC
- Tailscale in userspace mode
- only the node's /32 advertised
- managed through `pct exec` over node SSH, with no sshd in the container
- a single-use, tagged auth key that pmox never stores

## What Changes

- **New noun group `pmox router`.**
  - `create`:
    - ensures an Ubuntu LTS CT template, the newest in the node's appliance index, then creates an unprivileged container on the server's bridge, tagged `pmox-router`, started on boot and first in the startup order, protected, with a fixed MAC and DHCP (or `--ip`)
    - removes its sshd
    - installs Tailscale in userspace-networking mode
    - joins with an auth key read from `--auth-key-file`, `TS_AUTHKEY` or a paste, passed over stdin
    - advertises `<node ip>/32` (or `--route`)
  - `login`: joins without a key, showing Tailscale's login URL.
  - `route add|rm|list`: change or show the advertised routes in place (repeatable `--route` at create; private ranges only, no wider than /16; the node's /32 is removed only with `--force`)
  - `status`: container state, tailnet address, the route advertised vs approved vs the node's current address, and whether egress is filtered.
  - `delete`: `tailscale logout`, then destroys the container. It warns that a Mac at home still routing the /32 loses the node until it stops accepting the route.
- **`create --firewall`** turns on the container's guest firewall with an egress allowlist. pmox refuses unless the datacenter firewall is already enabled, and never enables it itself.
- **Node SSH can run commands.** `pvessh` gains `Run` with stdin, output streaming and the exit code, used for `pct exec`.
- **pmox's VM commands ignore containers.** Resolution, pickers, `list`, `access`, `yardo` and template lookups consider only QEMU VMs. Today a container's VMID resolves, and the qemu calls fail with an API error. VMID allocation and cleanup still see every guest.
- **Doctor:**
  - reports the router's state when one exists
  - when the node is unreachable while this machine is on the node's own subnet with the overlay up, names the "router down at home" lockout and its fix (`tailscale set --accept-routes=false`)
- **Docs.** `docs/remote-access.md` uses `pmox router` for the node, with the Tailscale policy to paste (tagOwners, an autoApprover for the /32, a grant limited to tcp 8006/22 via the router).

## Capabilities

### New Capabilities
- `overlay-router`: the `pmox router` commands and the router container's lifecycle, its Tailscale join, status and removal, the opt-in firewall, and the doctor checks about it.

### Modified Capabilities
- `cli-command-tree`: adds the `router` noun group.
- `pve-node-ssh`: node SSH can run a command, with stdin and the exit code.
- `list-info-lifecycle`: name/VMID resolution and listing consider QEMU VMs only.

## Impact

- **Code:**
  - `internal/pveclient`: LXC create, start, stop, status, delete and interfaces; aplinfo list and download; storage by content; the network bridge address; `Resource.Type`
  - `internal/pvessh`: `Run`
  - a new `internal/router` package, plus `cmd/pmox/router.go`
  - the resource filters in `internal/vm/resolve.go`, `pick.go`, `cmd/pmox/list.go`, `target_prompt.go`, `access.go`, `yardo_fleet.go`, `launch_template.go`, `template_list.go`, `hostinfo.go`
  - `cmd/pmox/doctor.go`, `cmd/pmox/tree.go`
- **Requirements:**
  - The router needs `node_ssh`: `pct` runs as root on the node. pmox already holds that credential.
  - The API token needs `Datastore.AllocateTemplate`, as well as what launch needs.
- **Scope note:** `openspec/config.yaml` lists LXC as out of scope for v1. This change adds the one pmox-managed container, and that note is narrowed to say so.
- **No stored secrets.** The auth key exists only in pmox's memory and, briefly, in a 0600 file on the container's tmpfs.
