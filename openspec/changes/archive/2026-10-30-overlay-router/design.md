# Design

## Context

See proposal.md. The state this builds on:
- **pmox reaches the node two ways.** It uses the PVE API with a token, and node SSH as root. Node SSH is SFTP only today (`internal/pvessh`: snippets, file reads and writes).
- **Guests come from one listing.** `ClusterResources("vm")` returns both QEMU guests and LXC containers, but `Resource` has no type field (`internal/pveclient/cluster.go:25`). So a container's VMID resolves through `vm.Resolve` today, and `pmox list --all` shows containers.
- **The token isn't root.** The user's token is `root@pam!…`. PVE treats a token as a different identity from `root@pam`, so root-only container settings are out of reach: `devN` device passthrough, raw `lxc.*` keys, and feature flags other than nesting.

## Goals / Non-Goals

**Goals:**
- One command creates a working router.
- The auth key is never written to pmox's files or a command line.
- Nothing on the host changes except one container. No packages, no sysctls.
- The router is easy to check (`status`, doctor) and to remove.

**Non-Goals:**
- NetBird routers. The `router` noun leaves room for an `--overlay netbird` later.
- Advertising the whole LAN.
- ProxyJump through the node for VMs that aren't on the tailnet.
- Managing the Tailscale policy (the docs give the snippet).
- Enabling the datacenter firewall.
- A choice of OS for the router.

## Decisions

**1. An LXC router, not Tailscale on the host.** The host's packages and sysctls stay untouched. The router can be removed in one command, and it works with a token.
*Alternative considered:* the host. It's less code, but it modifies the hypervisor.

**2. Userspace networking.**
- Kernel TUN in an unprivileged CT needs `/dev/net/tun` passed through, and `dev0` is root@pam-only. Userspace mode needs no device, no `ip_forward` and no iptables.
- Subnet routing works in userspace mode: tailscaled terminates each connection and opens a new one from the CT's own LAN address, so the node always replies over vmbr0.
- Throughput is ample for the API, SSH and rsync.
- `status` ignores tailscaled's "IP forwarding disabled" warning.

**3. Only the node's /32.**
- It limits what a tailnet member, or a compromised router, is offered through the route to one host. The Tailscale grant then narrows it to tcp 8006/22.
- On a café network using the home subnet, the /32 still wins by longest-prefix match.
- **Cost:** at home, the Mac also routes the node through the router (directly over the LAN, about 1 ms). If the router is down, the node is unreachable even from home. Hence the warning on delete, and doctor's lockout hint with `tailscale set --accept-routes=false`.
- **Where the address comes from:** the server's bridge address on the node (`GET /nodes/{node}/network`). It falls back to resolving the API host. A non-private or 100.64/10 result is refused, and `--route` overrides.

**4. `pct exec` over node SSH, no sshd in the CT.**
- **No listening service.** With the firewall off by default, a listening sshd on the LAN and tailnet is the attack surface that matters. `pct exec` uses the node SSH pmox already holds, so it adds no privilege.
- **Less to manage.** There is no host-key pinning and no DHCP churn.
- **It still works when the CT's network is broken.**
- **The new code:** `pvessh.Client.Run`, about 40 lines on the existing x/crypto/ssh connection: a session, stdin and the output streams, `*ssh.ExitError` mapped to an exit code, and the session closed when ctx is cancelled.
- **How remote commands are written:** as `pct exec <id> -- /bin/sh -c '<script>'`, with absolute paths and no terminal.
*Alternative considered:* SSH straight into the CT with the pmox key. Rejected for the attack surface, and because it needs the CT's IP.

**5. How the auth key travels.**
- **Precedence:** `--auth-key-file`, then `TS_AUTHKEY`, then a hidden prompt on a terminal.
- **In the CT:** sent over `Run`'s stdin to `sh -c 'umask 077; cat > /run/pmox-ts.key'`, used as `tailscale up --auth-key=file:/run/pmox-ts.key`, then removed (`/run` is tmpfs).
- **Recommended key:** tagged (`tag:pmox-router`), pre-approved, single-use and **not** ephemeral. An ephemeral router would disappear after a long host outage, and pmox has no stored key to rejoin with. Tagged devices don't expire.
- **Tags are optional.** pmox never passes `--advertise-tags` itself (the key carries the tag). An untagged router works, but has key expiry on. Status and the docs say to disable expiry for it in the admin console.
- **Route approval:** an `autoApprovers` entry for the /32 approves it with no click.

**6. Ubuntu LTS, with no choice of OS.**
- The router uses the newest Ubuntu LTS standard template in the node's appliance index: `ubuntu-YY.04-standard` with an even `YY`, the highest version, never a hardcoded name. That's the same OS family as pmox's VMs: the same apt and unattended-upgrades setup, and the same Tailscale package.
- If the index has no Ubuntu LTS, it uses the newest `debian-*-standard`, with a note, so a stale index never blocks the router.
- There is no flag: it's just a router.

**7. Container shape.**
- `POST /nodes/{node}/lxc` with:
  - `ostemplate=<vztmpl storage>:vztmpl/<template>`, downloaded through `POST aplinfo` when it's missing
  - `unprivileged=1`, `features=nesting=1` (systemd in current releases needs it)
  - `net0=name=eth0,bridge=<bridge>,hwaddr=<fixed>,ip=dhcp` (or `--ip`), with `firewall=1` only under `--firewall`
  - `rootfs=<rootdir storage>:3`, `memory=256`, `swap=256`, `cores=1`
  - `onboot=1`, `startup=order=1,up=10`, `protection=1`, `tags=pmox-router`
  - no `ssh-public-keys`, no password
- **Storage:** the server's `storage` when it accepts `rootdir`, else the first enabled one that does; for the template, the first enabled storage with `vztmpl`.
- **VMID:** from `/cluster/nextid`, retried once on a conflict.
- **Fixed MAC:** generated so the user can reserve the address in DHCP. Without `--firewall`, pmox never needs the CT's IP.

**8. Setup inside the CT** (one `pct exec` script, idempotent):
- `systemctl disable --now ssh`, then `apt-get purge -y openssh-server`
- unattended-upgrades covering the distribution and pkgs.tailscale.com
- `apt-get update`, then `curl` and `ca-certificates` (the Ubuntu template ships without `curl`)
- postfix purged (the template ships it, listening on localhost)
- Tailscale from its install script, which adds its apt repository
- `FLAGS="--tun=userspace-networking"` in `/etc/default/tailscaled` (the unit's `EnvironmentFile`), then a restart

The join runs separately (`tailscale up --reset` with the spec's flags), so `login` reuses it.

**9. Status sources.**
- **The container:** the API (`/lxc/{id}/status/current`, `/config`).
- **Tailscale:** `tailscale status --json` through `pct exec`:
  - the state from `BackendState`
  - the address from `Self.TailscaleIPs`
  - approved routes are `Self.AllowedIPs` minus the Tailscale IPs, and primary routes come from `Self.PrimaryRoutes`
- **Advertised routes:** `tailscale debug prefs`. Missing fields show as "unknown", since these are local-API fields.
- **Egress:** filtered when `net0` has `firewall=1` and the datacenter firewall is on.

**10. The opt-in firewall.**
- **Pre-check:** `GET /cluster/firewall/options` must report `enable=1`, or pmox refuses with the lockout explanation.
- **Setup:** pmox writes the guest's options (`enable=1`, `policy_out=DROP`, `ipfilter=1`, `macfilter=1`) and the outbound rules through `/nodes/{node}/lxc/{id}/firewall/...`.
- **`--ip` is required,** because `ipfilter` needs a known address.

**11. Filtering containers out of VM commands.**
- `Resource` gains a `Type` field and `IsQEMU()`. The filter applies at the call sites listed in the proposal.
- VMID reservation (`internal/template/vmid.go`) and cleanup's set of existing VMIDs stay unfiltered.
- The router has only the `pmox-router` tag, and `HasTag` matches whole tags, so cleanup's orphan checks never treat it as a pmox VM.

## Risks / Trade-offs

- **[Risk]** The router is a single point of failure while away. → `onboot`, `startup=order=1`, `protection=1`, unattended-upgrades, and checks in status and doctor.
- **[Risk]** The home lockout when the router is down (Decision 3). → Delete warns about it, and doctor's hint names the fix.
- **[Risk]** A compromised router can reach the whole LAN at layer 2, and Tailscale grants don't bind it. → No listening services, auto-updates, and the opt-in firewall. The docs say so.
- **[Risk]** SNAT hides who connected: the node sees only the CT's address. → Accepted and documented.
- **[Risk]** Older reports say tokens can't set `nesting` at create. → If create fails on `features`, retry without it and warn. The integration check verifies this on the user's PVE.
- **[Risk]** `pct exec` stdin passing needs a check on the user's PVE. → A spike task before implementation.
- **[Trade-off]** LXC enters pmox's scope for this one container. The config note is narrowed accordingly.

## Migration Plan

Nothing to migrate. The container filter changes behaviour only for clusters with containers: they disappear from VM commands, which is the intended fix. To roll back the router itself, run `pmox router delete`.

## Spike results (2026-10-30, p0, Ubuntu 24.04 template, Tailscale 1.104.4)

1. **Token + `features=nesting=1` at create:** accepted.
2. **`pct exec` over node SSH (no terminal):**
   - stdin reaches the command (a 22-byte key written as mode 600 in `/run`)
   - exit codes come back (status 3)
   - the SFTP + `pct push` fallback also works
   - **Bug found and fixed:** passing the same `bytes.Buffer` as stdout and stderr lost output (concurrent `ReadFrom`). `pvessh.Run` now serializes a shared writer.
3. **The CT's IP:** `/lxc/{id}/interfaces` returned `eth0 192.168.0.37/24` within seconds of start, and `ip -4 addr` via `pct exec` works too.
4. **Userspace routing of the /32:**
   - after approval, the API and node SSH work through the router at home (Mac → utun → router, direct over the LAN, 3 ms) and from a phone hotspot (`pmox list`)
   - status fields as designed: `AllowedIPs` and `PrimaryRoutes` include the route once approved, and `tailscale debug prefs` gives `AdvertiseRoutes`
5. **`tailscale logout` does not remove the device.** It went offline but stayed in the tailnet, so its route stopped applying (the Mac went back to en0). Delete therefore always names the device to remove in the admin console. Otherwise the next router joins as `pmox-router-<node>-1`.

## Open Questions

- Should `pmox router create` print the Tailscale policy snippet with the node's address filled in? Cheap to add, and it doesn't change the specs.
