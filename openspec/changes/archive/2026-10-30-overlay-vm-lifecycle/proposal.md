# Proposal

## Why

`address: overlay` (remote-access-overlay) makes pmox reach a running VM at its Tailscale or NetBird address. Two gaps remain in the VM's lifecycle:
- **Starting a stopped VM from away fails.** `shell` and `exec` start the VM, then wait on its LAN address.
- **`list` doesn't show which address is the overlay one.**

The Proxmox API stays reachable away from home with no pmox change: Tailscale on the node advertises the node's own address as a /32 route. That goes in the guide.

Joining a VM to the tailnet stays with yardo (its Logins & keys item; see yardo's `remote-access` change). pmox only consumes the address.

## What Changes

- **Start waits for the overlay.** With `address: overlay`, a stopped VM that `shell` or `exec` starts is waited on until it reports a `tailscale*` or `wt*` address (bounded), and SSH readiness is checked on that address.
  - A VM that never joins falls back to its LAN address, with a note.
  - `launch` is unchanged, since a new VM can't have joined yet.
- **list marks the overlay address.** A `(ts)` or `(nb)` suffix in the IP column; plain JSON stays the address alone.
- **vm info marks the connect address.** It says which address pmox connects to, among the interfaces it already lists.
- **Docs.** `docs/remote-access.md` gains the /32 route for the node, these behaviours, and how to remove a deleted VM's device.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `ssh-shell-exec`: auto-start waits for the overlay address under `address: overlay`.
- `list-info-lifecycle`: list marks overlay addresses; info names the address pmox connects to.

## Impact

- **Code:**
  - `cmd/pmox/ssh.go` (`getOrStartVM`)
  - `internal/vmwait` (wait for an overlay address)
  - `cmd/pmox/list.go` and `internal/vm/table.go`
  - `cmd/pmox/info.go`
  - `docs/remote-access.md` and the README
- **No new config, no credentials.**
- **Not in scope: leaving the tailnet on `pmox delete`.** A deleted VM's device stays in the tailnet until it is removed in the admin console, or until it expires as an ephemeral key. Left for later, if it becomes a problem.
- **Depends on** `remote-access-overlay` (the `address` setting, shipped in v0.48.1).
