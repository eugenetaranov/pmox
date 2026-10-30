# Proposal

## Why

pmox reaches VMs at the LAN IPv4 that the guest agent reports, and reaches PVE at a LAN URL. That works only on the home network. Away from home you need an overlay (Tailscale or NetBird), and today pmox handles it by accident:
- `PickIPv4` doesn't skip `tailscale0` or `wt0`, so it picks the LAN IP only because that interface comes first.
- `doctor` says "firewall?" when the real cause is "you're not on the home LAN".
- `cleanup known-host` prunes the host-key pins of a VM's second address.

There is also no guide to connecting from a laptop: SSH, Docker, the MCPJungle gateway.

## What Changes

- **Address preference per server.** A new `address: lan|overlay` key in the server config, defaulting to `lan`.
  - `overlay` makes every guest command (`shell`, `exec`, `cp`, `sync`, `mount`, `ssh-config`, `list`, yardo fleet, delete) use the VM's Tailscale or NetBird IPv4, when the VM has one.
  - A session to that address survives moving between home and away.
  - The choice is static: no probing, no racing dials.
- **IP picker.** In `lan` mode, `tailscale*` and `wt*` interfaces are skipped, so the LAN IP is chosen deliberately.
- **cleanup known-host.** The set of live VM IPs is built from every IPv4 the guest agent reports, not one picked address. Pins under a VM's overlay address are no longer pruned and silently re-trusted.
- **doctor.**
  - When the API is unreachable at a private address, the hint names the likely cause: not on the home LAN, or the current network uses the same subnet as home.
  - A new warning appears when `address: overlay` is set but this machine has no overlay address.
- **Docs.** A new `docs/remote-access.md`:
  - prerequisites: CGNAT check, renumbering the home LAN, a scoped API token
  - topologies: subnet router; agent on each machine; both
  - Tailscale and NetBird setup, and how to verify a direct LAN path at home
  - connecting from a laptop: `pmox shell`, `ssh-config` and VS Code, Docker over an SSH context or the TLS API, the MCPJungle gateway over the overlay or an SSH tunnel
  - mosh/tmux for long shells
  - troubleshooting
  - README and llms.txt link to it.

## Capabilities

### New Capabilities
- `remote-access`: how pmox chooses a VM's address across a LAN and an overlay network (the `address` server setting), the doctor checks for reachability from off the LAN, and the remote-access guide.

### Modified Capabilities
- `launch-default`: IP picker heuristic. `tailscale*` and `wt*` join the skipped interface prefixes in `lan` mode, and `overlay` mode prefers them.
- `ssh-shell-exec`: guest agent IP discovery honours the server's `address` setting.
- `cleanup`: stale known_hosts detection treats every IPv4 a running VM reports as live.

## Impact

- **Code:**
  - `internal/config/config.go`: new `Server.Address` field
  - `internal/vmwait/ip.go`: `PickIPv4` gains a preference, plus a new `AllIPv4`
  - the `PickIPv4` callers: `cmd/pmox/ssh.go`, `mount.go`, `sshconfig.go`, `list.go`, `yardo_fleet.go`, `delete.go`, `cleanup.go`
  - `cmd/pmox/doctor.go`
  - new `docs/remote-access.md`; README and llms.txt
- **Config:** additive and optional. Existing configs behave as before, except that `lan` mode now always skips overlay interfaces.
- **No new dependencies.** pmox doesn't install, join or authenticate Tailscale or NetBird. That stays with tack's `tailscale` role and yardo, and pmox stores no overlay keys.
- **Related project:** yardo already prints the VM-side connection steps (`sudo yardo connect`). The guide points to it rather than duplicating its logic.
