# Reaching your VMs from away

pmox talks to Proxmox over its API (port 8006) and to VMs over SSH, at
the addresses Proxmox reports. At home that's the LAN. Away from home
you need an overlay network: [Tailscale](https://tailscale.com) or
[NetBird](https://netbird.io). This page sets one up so pmox works the
same from a café as it does at home, and switches by itself when you
walk in the door. It then covers connecting a laptop's SSH, Docker and
MCP clients to a VM.

- [1. Before you start](#1-before-you-start)
- [2. Pick a topology](#2-pick-a-topology)
- [3. Set up Tailscale](#3-set-up-tailscale)
- [4. Or set up NetBird](#4-or-set-up-netbird)
- [5. Check the path](#5-check-the-path)
- [6. Point pmox at it](#6-point-pmox-at-it)
- [7. Shell and SSH](#7-shell-and-ssh)
- [8. Docker from your laptop](#8-docker-from-your-laptop)
- [9. The MCP gateway from your laptop](#9-the-mcp-gateway-from-your-laptop)
- [10. Troubleshooting](#10-troubleshooting)

## 1. Before you start

- **Renumber the home LAN to an uncommon subnet.** For example
  `10.83.17.0/24` rather than `192.168.0.0/24` or `192.168.1.0/24`.
  Hotels, cafés and phone hotspots use the common ones. When the network
  you're on has the same addresses as home, no overlay can tell them
  apart, and "your" `192.168.1.20` is a stranger's machine. Give the
  Proxmox host a fixed address or a DHCP reservation.
- **Check for CGNAT.** Compare the WAN address on your router with what
  an external "what's my IP" site shows.
  - If they differ, your ISP shares one public address between many
    customers, and only a mesh overlay (Tailscale, NetBird) can get in.
  - If they match and you'd rather not involve a third party, your
    router's own WireGuard server, with the macOS WireGuard app's
    on-demand rules turning it off on your home Wi-Fi, also works.
    pmox needs nothing special for that: it is topology A below.
- **Never port-forward 8006 to the internet.** Use a scoped API token
  (see [pve-setup.md](./pve-setup.md#2-role-and-privileges)) and TOTP on
  the web UI.

## 2. Pick a topology

**A. A subnet router.** One small machine on the home LAN, such as an LXC
on `vmbr0` or the router itself, advertises the LAN to the overlay. pmox
keeps using LAN addresses and needs no config change.
- **Away:** traffic goes through the overlay.
- **At home:** it goes straight over the LAN.
- **The catch:** a connection opened in one place breaks when you move,
  because the route changes underneath it.
  - `pmox mount` notices and carries on syncing by itself.
  - An open `pmox shell` drops. Use mosh, or tmux on the VM, for long
    sessions.

**B. An agent on each machine (recommended).** Tailscale or NetBird runs on
the Proxmox host and on the VMs you keep. Each one gets an overlay address
(`100.x.y.z`) that stays the same wherever you are. Set `address: overlay`
(§6) and pmox connects to that address.
- **At home:** the overlay finds the direct LAN path by itself, so nothing
  goes through a relay.
- **Away:** the same address works over the internet.
- **Moving:** sessions and mounts survive it, with a few seconds' stall.

**Use both** when you also launch throwaway VMs: B for the host and the VMs
you keep, plus A's subnet router for the rest. A newly launched VM has no
overlay address until it joins, so launching one while away needs A's
route to the VM bridge.

Either way, the access rules should let your laptop reach:
- the Proxmox host on TCP 8006 and 22
- the VMs on TCP 22
- the ports in §8 and §9, if you use them

Nothing else. Leave key expiry on for the laptop. Turn it off only for the
router and the host.

## 3. Set up Tailscale

The free Personal plan covers a homelab. One limit: the official client is
signed in to one tailnet at a time. If your laptop already uses your
employer's tailnet, switching to your own takes the work one down (and the
other way round), so consider NetBird (§4).

1. **Subnet router (A).** In an LXC on `vmbr0`:
   ```
   curl -fsSL https://tailscale.com/install.sh | sh
   echo 'net.ipv4.ip_forward = 1' | sudo tee /etc/sysctl.d/99-tailscale.conf && sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
   sudo tailscale up --advertise-routes=10.83.16.0/23 --advertise-tags=tag:homelab
   ```
   - **Approve the route** in the admin console. macOS accepts approved
     routes by itself; Linux laptops need `--accept-routes`.
   - **Why a /23:** advertising a range one size wider than the LAN
     (`10.83.16.0/23` covers `10.83.17.0/24`) means the LAN's own, more
     specific route wins while you're home, and the overlay route wins
     away.
2. **The Proxmox host (B).** Install it with
   `curl -fsSL https://tailscale.com/install.sh | sh`, then pick one:
   - **Keep the LAN URL (recommended).** Advertise just the host's own
     address as a route:
     ```
     echo 'net.ipv4.ip_forward = 1' > /etc/sysctl.d/99-tailscale.conf && sysctl -p /etc/sysctl.d/99-tailscale.conf
     tailscale up --advertise-routes=10.83.17.2/32
     ```
     Then approve the route in the admin console.
     - pmox's URL, saved secret, certificate pin and host-key pin all stay
       as they are.
     - A `/32` is more specific than any network's `/24`, so it reaches
       the host even on a café network that uses your home subnet.
   - **Use its MagicDNS name.** Run `tailscale up` as a plain node (no
     routes, no forwarding), and use `pve.<tailnet>.ts.net` as the URL.
     pmox keys its saved secret and pins by URL, so this means running
     `pmox init` again for the new URL.
3. **VMs (B).**
   - In `sudo yardo` on the VM, pick **Tailscale**, then **Tailscale
     login**, and paste an auth key.
   - Or add tack's `tailscale` role to your playbook (`pmox apply --init`)
     and run `pmox apply web1`.
   - Mint the keys in the admin console as **tagged** (`tag:pmox-vm`) and
     **pre-approved**. Make them **ephemeral** for throwaway VMs, so a
     deleted VM's device goes away by itself.
   - `pmox delete` doesn't remove a VM's device from the tailnet. A device
     joined with an ephemeral key goes away by itself. Otherwise remove it
     in the admin console, or the next VM with that name joins as
     `web1-1`.
   - pmox stores no auth keys.
4. **MagicDNS.** Turn it on, so `web1` and `pve` resolve on the laptop
   everywhere.

## 4. Or set up NetBird

NetBird is open source end to end: you can run its management server
yourself.
- Run it on a small VPS, never at home: a control plane behind your home
  connection goes down exactly when you need it.
- Or use NetBird Cloud.

1. **A routing peer (A).** In an LXC on `vmbr0`, install NetBird and run
   `netbird up --setup-key <key>`. In the dashboard, add a network
   resource for the home LAN (`10.83.17.0/24`) that routes through that
   peer, with masquerade on.
2. **The Proxmox host and VMs (B).**
   - Install NetBird and join with a setup key: in `sudo yardo` on a VM,
     pick **NetBird** and log in under This VM → Logins & keys.
   - Use a reusable setup key that auto-assigns a `vms` group and expires.
     Make it ephemeral for throwaway VMs.
3. **Access policies.**
   - Delete the default allow-all policy.
   - Allow `laptop` → `pve` on TCP 8006/22, and `laptop` → `vms` on TCP 22
     plus the ports you use from §8–§9.
4. **Running Tailscale on the same laptop.**
   - Move NetBird's network to another range under its network settings
     (for example `10.250.0.0/16`), since both use `100.64.0.0/10` by
     default.
   - The `net.overlay` check in `pmox doctor` (§6) looks for a
     `100.64.0.0/10` address only, so it then warns even when NetBird is
     up; ignore that warning.
5. **When you're home:** check with `netbird routes list`, and
   `route get 10.83.17.2` on macOS, whether the client drops the LAN route
   (and goes direct). Don't rely on it until you've seen it on your own
   laptop.

## 5. Check the path

At home, with Tailscale on both ends:

```
tailscale ping web1      # "pong from web1 (100.x) via 10.83.17.20:41641" = direct over the LAN
tailscale status         # each peer shows "direct <ip:port>" or "relay"
```

If it says `via DERP(…)` at home, something blocks the direct path:
- guest Wi-Fi with client isolation
- the laptop and VMs on different VLANs
- ufw on the VM: allow UDP 41641 from the LAN (`sudo ufw allow from 10.83.17.0/24 to any port 41641 proto udp`)

The first ping or two after waking can go via a relay before the direct
path takes over; that's normal.

Then try it away: tether to your phone's hotspot and run `pmox list`
and `pmox shell web1`.

## 6. Point pmox at it

**The Proxmox URL.**
- Keep one URL that works at home and away:
  - the LAN address (`https://10.83.17.2:8006`), with a subnet router (A)
    or the host advertising its own `/32` (§3)
  - or the host's MagicDNS name (`https://pve.<tailnet>.ts.net:8006`)
- Don't add a second context for "away". It would be a second set of pins
  and secrets for the same cluster.
- A pinned self-signed certificate (`insecure: true` with `tls_pin_sha256`)
  stays valid under any name. With `insecure: false`, the name has to be in
  the certificate: `tailscale cert` on the host gets you one.

**The address (topology B).** Under the server in `config.yaml`:

```yaml
servers:
  https://pve.tailnet-1234.ts.net:8006:
    address: overlay   # lan (default) | overlay
```

With `overlay`, the commands that reach a VM over SSH connect to the VM's
Tailscale or NetBird address whenever it has one, and to its LAN address
otherwise:
- `pmox shell`, `exec`, `cp`, `sync`, `mount`
- `vm ssh-config`
- `yardo apply` and `yardo verify`

`pmox list` shows the same address, marked ` (ts)` for Tailscale or
` (nb)` for NetBird. `pmox vm info` names it on its `SSH:` line. pmox
doesn't try both addresses or guess where you are:
- If the overlay address doesn't answer, the command fails.
- With `lan` (the default), pmox always picks the LAN address, even on a
  VM that has joined the overlay.

**Starting a stopped VM.** When `shell` or `exec` starts one, pmox waits
for its LAN address, then up to 90 seconds more for its Tailscale or
NetBird address (Tailscale comes up a few seconds after the network), and
connects to that.
- A VM that is logged out of Tailscale never gets one, so pmox uses its
  LAN address, with a note.
- `launch` always uses the LAN address: a new VM hasn't joined yet.
  Launching from away therefore needs a route to the VM bridge (A).

**Host keys.** pmox pins each VM's host key under the address it connects
to.
- After switching a server to `overlay`, the first connection to each VM
  pins its key again under the `100.x` address.
- `pmox cleanup` keeps the pins for every address a running VM reports,
  so it never prunes the overlay pin.

**`pmox doctor`.**
- When it can't reach a Proxmox host on a private address, it says so: off
  the home LAN without a route, or on a network that uses the same
  addresses as home.
- With `address: overlay`, it warns when this machine has no overlay
  address (`net.overlay`).

## 7. Shell and SSH

```
pmox shell web1                          # interactive
pmox shell web1 -L 3000                  # plus a port forward
pmox vm ssh-config web1 >> ~/.ssh/config # then: ssh web1, VS Code Remote-SSH, rsync, Ansible
```

**The `ssh-config` block.**
- It uses the address pmox would connect to, and pmox's own known-hosts
  file.
- Regenerate it after changing `address`, or after the VM's LAN address
  changes. The saved `HostName` doesn't update itself.

**Long sessions.** For a shell you want to keep across a move, run tmux on
the VM and reattach, or use mosh. `mosh` needs UDP 60000–61000 open;
yardo's ufw item opens it when you pick mosh.

## 8. Docker from your laptop

**Over SSH (recommended).** No port to open and no certificates: your SSH
key is the login.

```
pmox vm ssh-config web1 >> ~/.ssh/config
docker context create web1 --docker "host=ssh://web1"
docker context use web1          # back to local: docker context use default
docker ps                        # runs on web1
```

- **Who logs in:** the login user (pmox's `user`, `pmox` by default) must
  be in the VM's `docker` group. yardo's Docker item adds the user who
  ran `sudo yardo`. If that user differs, use
  `pmox vm ssh-config web1 --user <them>`.
- **Paths:** `-v ./src:/src` mounts the **VM's** `./src`, not your laptop's.
  Sync the code first (`pmox mount` or `pmox sync`).
- **Ports:** `-p 3000:3000` opens the port on the VM. Reach it at the
  VM's address: `http://web1:3000` with MagicDNS, or the LAN address
  through the subnet router.
- **Ports bypass ufw:** Docker's published ports skip ufw, so a published
  port is open to everything that can reach the VM. To keep one private,
  either:
  - publish it on loopback (`-p 127.0.0.1:3000:3000`) and tunnel it:
    `pmox shell web1 -L 3000`, then `http://localhost:3000`
  - or limit it in `sudo yardo`, under This VM → containers' published
    ports, to the VPNs or this machine only

**Over TLS (port 2376).** For tools that can't use SSH:
1. In `sudo yardo`, turn on This VM → Who can reach it → Docker API. It
   opens port 2376, accepting client certificates only, from the LAN or
   the VPNs, never the internet.
2. On the VM, run `sudo yardo-docker client laptop`. It prints the
   laptop's certificate and the exact `docker context create web1-tls
   --docker "host=tcp://<addr>:2376,ca=…,cert=…,key=…"` line.

`sudo yardo connect` on the VM prints all of this for that VM, using its
overlay address first.

## 9. The MCP gateway from your laptop

yardo's MCPJungle gateway serves `http://<host>:<port>/mcp`:
- **Port:** 8080 for the first user set up on the VM, 8081 and up for
  the others (`sudo yardo connect` shows yours).
- **Who can reach it:** set in `sudo yardo` → This VM → MCP gateway.

**This machine only (the default, no token).** Tunnel to it:

```
pmox shell web1 -L 8080          # or: ssh -N -L 8080:127.0.0.1:8080 web1
claude mcp add --transport http web1 http://127.0.0.1:8080/mcp
```

- **Use this with topology A.** The VM has no overlay interface, and
  the gateway's "VPNs" setting only admits traffic that arrives on one.
- **It drops when you move.** The tunnel lives as long as that SSH
  session, so reopen it after moving between home and away.

**From your VPNs (topology B, with a token).**
1. In `sudo yardo`, set the MCP gateway to your VPNs, or to the Tailscale
   address only. Outside the VM it then needs a token.
2. On the VM, run `mcp-sync client laptop`. It makes the laptop's token.
3. On the laptop:
   ```
   claude mcp add --transport http web1 http://web1.<tailnet>.ts.net:8080/mcp \
     --header "Authorization: Bearer <token>"
   ```
   For Codex, in `~/.codex/config.toml`:
   ```toml
   [mcp_servers.web1]
   url = "http://web1.<tailnet>.ts.net:8080/mcp"
   http_headers = { "Authorization" = "Bearer <token>" }
   ```

- **Use the overlay name at home too.** A gateway on the VPNs isn't open
  on the VM's LAN address.
- **Moving is fine.** Each MCP request is its own HTTP call, so moving
  between home and away needs nothing redone.
- **Exact lines:** `sudo yardo connect` prints them for that VM and user.

## 10. Troubleshooting

### `pmox doctor`: "… is a private address: not on the home LAN?"

You're away and nothing routes to the home network. Check that:
- the overlay is up on the laptop (`tailscale status`, `netbird status`)
- the subnet route is approved and accepted
- the node is on the overlay, for topology B

### `pmox doctor`: "this network … uses the same addresses as the Proxmox host"

You're on a network with the same addresses as home. Renumber the home
LAN (§1). Until then, use topology B's overlay addresses, which can't
clash.

### Works at home, not away (or the other way round)

- **The LAN address (topology A):** check the subnet route.
- **`address: overlay` (topology B):** check the VM has joined the overlay
  (`tailscale status` on the VM), and run `pmox doctor` for the
  `net.overlay` check.
- **Tailscale in userspace networking mode** (containers, some LXCs) has
  no `tailscale0` interface. pmox can't see its address and uses the LAN
  address.

### Only one tailnet at a time

The official Tailscale client is signed in to one tailnet at a time.
Switching to a work tailnet stops `pmox mount` syncing until you switch
back. Use NetBird for the homelab (§4), or keep the homelab on another
machine.

### Corporate VPNs

A full-tunnel corporate VPN (Zscaler, GlobalProtect, Cisco) can take over
the routes and DNS that the overlay needs. Split-tunnel exceptions are
up to your IT department.

### Slow large copies

The overlay's MTU is 1280 bytes, which is fine for SSH and rsync. Bulk
copies run a little slower than over the raw LAN.
