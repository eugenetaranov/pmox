# Design

## Context

Every guest command finds its target by asking PVE for the guest agent's interfaces and calling `vmwait.PickIPv4`. The call sites are:
- `cmd/pmox/ssh.go:400`
- `mount.go:413`
- `sshconfig.go:122`
- `list.go:97`
- `yardo_fleet.go:227`
- `delete.go:410`
- `cleanup.go:370`

So the PVE API must be reachable before any VM is: whatever the overlay, the node itself has to be on it, either through a subnet route or with its own agent.

Guest host keys are pinned per IP (`ssh.go:466-505`). `ssh-config` writes the picked IP as `HostName`, along with pmox's own known-hosts options.

On the VM side, yardo already handles the overlay:
- its MCPJungle gateway can listen on loopback, on the Tailscale address, on the VPNs, everywhere, or publicly
- the Docker API over TLS can be opened on port 2376
- `sudo yardo connect` prints the laptop-side commands, using the VM's VPN address first

The panel discussion behind this change found that sessions survive moving between home and away only when the destination is an overlay address. A subnet route is enough to reach the cluster, but every session breaks on the move.

## Goals / Non-Goals

**Goals:**
- One config line (`address: overlay`) for users who run Tailscale or NetBird on their VMs.
- Deterministic address choice in both modes.
- Doctor messages that name the actual off-LAN failure.
- A guide that takes a user from "it works at home" to "Docker and MCP work from a café".

**Non-Goals:**
- pmox installing, joining or removing overlay devices, or holding auth keys. That stays with tack's `tailscale` role, yardo, and keys the user pastes. pmox has no credential store for this.
- Automatic home/away detection, and probing several addresses.
- Managing ACLs, routes or DNS in the Tailscale or NetBird admin consoles.
- Ephemeral-device cleanup through the Tailscale API when a VM is deleted (possible later).

## Decisions

**1. A static `address: lan|overlay` per server, not `auto`.**
- `auto` would mean racing two dials on every `shell` or `exec` and keeping two host-key pins per VM.
- Under `auto`, the same command could take a different path depending on timing, which is hard to debug.
- Static is predictable. The overlay address is the same at home and away, so a static choice loses nothing. At home Tailscale and NetBird find the direct LAN path on their own (check with `tailscale ping`).
- *Alternative considered:* a `--address` flag on every command. Left out for now; a second server context gives the same result.

**2. Recognise overlay interfaces by name (`tailscale*`, `wt*`), not by address range.**
- A NetBird account can use any CIDR, not just 100.64.0.0/10.
- Some ISPs use 100.64.0.0/10 for carrier-grade NAT.
- Interface names come from the guest agent and are stable.
- *Trade-off:* Tailscale in userspace-networking mode has no `tailscale0`, so pmox falls back to the LAN address. The guide says so.

**3. `lan` mode also skips overlay interfaces.**
- Today pmox gets the LAN address only because the guest agent lists LAN interfaces first.
- Adding the two prefixes to `skipPrefixes` for `lan` mode makes the choice deliberate.
- The fallback pass still returns the overlay address when it is the VM's only address.

**4. `launch`, `start` and the reachability wait (`WaitForIP`) keep using the LAN address.**
- A newly cloned VM joins the overlay only after its first SSH (through the tack role or yardo), so there is no overlay address to wait for yet.
- The first connection afterwards pins the overlay address once.
- *Consequence:* launching a VM from away from home still needs a subnet route to the VM bridge. The guide recommends a subnet router alongside per-VM agents for this reason, and for throwaway VMs.

**5. `AllIPv4` for cleanup.**
- Building the live set from one picked address makes the overlay pins look stale.
- Pruning them leads to a silent `accept-new` re-pin on the next connection, which quietly resets trust.
- The live set becomes every usable IPv4 on every running VM.

**6. Doctor checks are hints and warnings, with no network probing beyond what already runs.**
- The `api.reachable` hint branches when the host is RFC1918.
- Detecting an overlapping subnet only compares local interface addresses with the API host.
- The overlay-down check looks for any local 100.64.0.0/10 address and is only a warning, because NetBird can use other ranges.

**7. The guide is the main deliverable.**
- It recommends Tailscale by default.
- It recommends NetBird for a self-hosted control plane, or when the laptop already uses a work tailnet (the official Tailscale client runs one tailnet at a time).
- Router WireGuard is mentioned for homes with a public IP.

## Guide outline (`docs/remote-access.md`)

1. **Before you start.**
   - Check for CGNAT: compare the router's WAN IP with an external what's-my-IP.
   - Renumber the home LAN away from 192.168.0/24 and 192.168.1/24, for example to 10.83.17.0/24, with DHCP reservations for PVE.
   - Use a scoped PVE token and TOTP on the web UI. Never port-forward 8006.
2. **Pick a topology.**
   - **A. Subnet router only.** One LXC on vmbr0 advertises the LAN.
     - pmox needs no config change.
     - Sessions break when moving between home and away. Mounts recover through the mount daemon's retry; shells need mosh or tmux.
     - Advertise a /23 over the /24 LAN so the LAN route wins at home.
   - **B. Agent on the PVE node and on long-lived VMs, plus A for the rest (recommended).**
     - Set `address: overlay`.
     - Sessions survive the move.
     - Join VMs with tagged, pre-authorised, preferably ephemeral keys.
   - Access rules: the laptop reaches PVE on tcp 8006/22 and the VMs on tcp 22, plus the gateway and Docker ports below. Key expiry stays on for the laptop and off for tagged nodes.
3. **Tailscale steps:** the LXC router, `tailscale up --advertise-routes`, the node agent, the VM role (`pmox apply web1 -t tailscale`), MagicDNS.
   **NetBird steps:** a self-hosted server on a VPS (never at home) or NetBird Cloud, a routing peer, setup keys, a custom range if the laptop also runs Tailscale, `netbird routes list`.
4. **Check the path.**
   - At home: `tailscale ping web1` should answer `via <LAN ip>:41641`. If it answers via DERP, look at guest Wi-Fi, VLANs, or allow UDP 41641 in ufw.
   - Also check from a phone hotspot.
5. **pmox config:** `address: overlay`, the PVE URL as one name that resolves at home and away (no second context), and `pmox doctor`.
6. **Shell and SSH.**
   - `pmox shell web1`.
   - `pmox vm ssh-config web1 >> ~/.ssh/config` for VS Code, rsync and Ansible.
   - Regenerate the block after changing `address`, because its `HostName` changes.
   - mosh or tmux for long sessions.
7. **Docker from the laptop.**
   - **Over SSH (recommended; no open port, the SSH key is the login):**
     ```
     pmox vm ssh-config web1 >> ~/.ssh/config
     docker context create web1 --docker "host=ssh://web1"
     docker context use web1        # back: docker context use default
     ```
     - The login user must be in the VM's `docker` group (yardo's Docker item adds its user). Otherwise use `--user` on `ssh-config`.
     - `-v` paths are the VM's paths.
     - `-p` ports open on the VM. Reach them at the VM's overlay or LAN address.
     - Docker's published ports bypass ufw. To keep a port private, publish it on `127.0.0.1:3000:3000` and tunnel it with `pmox shell web1 -L 3000`, or use yardo's Docker ports setting (VPNs or this machine only).
   - **Over TLS** (for tools that can't use SSH): enable yardo's Docker API item (port 2376, client certificates, LAN or VPNs only). Run `sudo yardo-docker client laptop` on the VM, then `docker context create web1-tls --docker "host=tcp://<addr>:2376,ca=…,cert=…,key=…"`.
8. **MCPJungle gateway from the laptop.** Port 8080 for the first VM user, then 8081 and up; path `/mcp`.
   - **Loopback only** (default, no token):
     ```
     pmox shell web1 -L 8080        # or: ssh -N -L 8080:127.0.0.1:8080 web1
     claude mcp add --transport http web1 http://127.0.0.1:8080/mcp
     ```
     The tunnel drops when you move between home and away. This is the right choice for topology A, because the VM has no overlay address to listen on.
   - **On the overlay** (topology B):
     - In `sudo yardo`, set This VM → MCP gateway to listen on the Tailscale address or the VPNs.
     - On the VM, run `mcp-sync client laptop` for a token.
     - On the laptop:
       ```
       claude mcp add --transport http web1 http://<web1 MagicDNS name or 100.x>:8080/mcp \
         --header "Authorization: Bearer <token>"
       ```
     - Codex: a `[mcp_servers.web1]` table with `url` and `http_headers` in `~/.codex/config.toml`.
     - A gateway bound to the Tailscale address is not reachable at the LAN IP. Use the overlay name at home too.
   - `sudo yardo connect` on the VM prints these exact lines for that VM and user.
9. **Troubleshooting:**
   - doctor's off-LAN and overlap hints
   - the one-tailnet limit and corporate VPNs
   - MTU 1280
   - Tailscale in userspace mode (no `tailscale0`)
   - stale ssh-config `HostName`

## Risks / Trade-offs

- **[Risk]** Switching a server to `overlay` pins new host keys under the 100.x addresses on first connect (`accept-new`). → The guide says so. This is the same trust-on-first-use as any new VM, and cleanup now keeps both pins.
- **[Risk]** Saved `~/.ssh/config` blocks and Docker contexts keep the old `HostName`. → The guide says to regenerate them after changing `address`.
- **[Risk]** A VM is on the overlay but the laptop's client is down. With `overlay` set, pmox fails instead of falling back to the LAN address. → The doctor warning, and the error hint, suggest bringing the overlay up.
- **[Risk]** Topology A on a café network with the same subnet as home. → Renumbering the home LAN is the first step in the guide, and doctor detects the overlap.
- **[Trade-off]** Launching from away from home needs a subnet route. That's accepted (Decision 4).

## Migration Plan

Nothing to migrate: the key is optional and defaults to today's behaviour. The one visible change is in `lan` mode on a VM whose guest agent lists `tailscale0` or `wt0` before its LAN interface: pmox now picks the LAN address there, which is the intended fix. To roll back, remove `address:` from the config.

## Open Questions

- Should `pmox delete` later remove the VM's Tailscale device through the API, using a token the user provides? Deferred; doesn't affect this change.
