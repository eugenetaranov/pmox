# pmox cross-slice decisions

Decisions captured during exploration that span multiple OpenSpec changes.
Each entry is the contract that future slices implement against. If a slice
needs to deviate, it must update this file as part of its proposal.

---

## D-T1. Tag immediately after clone, before resize

**Slice**: `launch-default`

The launch state machine inserts a dedicated `set tags=pmox` API call
*immediately* after the clone task succeeds, before the resize call.
This closes the orphan-without-tag failure window: any cloned VM that
exists on the cluster after a failed launch will have the `pmox` tag
and can be cleaned up by `pmox delete` without `--force`.

The launch state machine is therefore 9 steps, not 8:

```
nextid → clone → tag → resize → config → start → wait-IP → wait-SSH → hooks
```

We do **not** auto-rollback on partial failure. If any step fails, the
VM stays on the cluster and the user runs `pmox delete <name>`. This
matches tack's "leave state, let the user clean up" principle.

**Cost**: one extra API round-trip per launch (~50ms on a healthy LAN).
**Benefit**: every failure mode after step 2 is cleanable via `pmox delete`.

---

## D-T2. Snippet storage validation is lazy

**Slice**: `cloud-init-custom`

`pmox configure` validates credentials against `/version` only. It does
**not** check whether the configured storage supports the `snippets`
content type. That check happens at `pmox launch --cloud-init <file>`
time, the first time it actually matters.

When the check fails, the error message must be actionable: name the
storage, name the missing content type, point at `/etc/pve/storage.cfg`,
suggest both fixes (edit storage config OR pass `--storage`), link to
the PVE wiki storage page.

Users who never use `--cloud-init` are never bothered.

---

## D-T3. IP discovery is qemu-guest-agent only

**Slice**: `launch-default`

pmox polls `GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`
to discover the VM's IP. **No DHCP-lease fallback** — the spec mentions
one, but it only works on `vmbr0` with PVE running its own dnsmasq, which
most homelabs don't.

If the agent isn't responding within `--wait`, fail with:

```
qemu-guest-agent not responding on VM <vmid>; install qemu-guest-agent
in your template and re-run launch
```

**Documented hard prerequisite**: the cloud-init template must have
`qemu-guest-agent` installed and `agent: 1` set on the template VM
config. The README and llms.txt both call this out in the "PVE-side
setup" section.

### IP picking heuristic (when the agent returns multiple)

```
1. Skip interfaces named: lo, lo0, docker*, br-*, veth*, cni*, virbr*, tun*
2. From remaining interfaces, pick the first IPv4 that is:
   - not in 127.0.0.0/8
   - not in 169.254.0.0/16
3. If nothing matches, fall back to the first non-loopback non-link-local
   IPv4 across all interfaces.
4. If still nothing, fail with: "qemu-guest-agent reported no usable
   IPv4 address; check the VM's network configuration".
```

A `--ip-from-interface eth1`-style flag is explicitly v2.

---

## D-T4. Server resolution logs the chosen server at -v

**Slice**: `server-resolution`

The 5-step precedence is unchanged:

```
1. --server flag
2. PMOX_SERVER env var
3. exactly one configured → use it
4. multiple configured + TTY → prompt
5. multiple configured + no TTY → error
```

When verbose mode is on (`-v`), every command (except `configure`) emits
one stderr line indicating which server was selected and why:

```
using server https://pve.home.lan:8006/api2/json (single configured)
using server https://pve.home.lan:8006/api2/json (PMOX_SERVER env var)
using server https://pve.work.lan:8006/api2/json (--server flag)
```

**Not** in v1: `pmox configure --set-default <url>` and a persisted
default-server pointer. If the multi-server case turns out to be
annoying, that becomes its own follow-up slice. Don't pre-build state
that may not be needed.

---

## D-T5. Drop --cloud-init-merge from v1

**Slice**: `cloud-init-custom`

The `cloud-init-custom` slice ships **only** plain `--cloud-init <file>`
with full-replace semantics. The user-supplied file becomes the VM's
entire user-data; pmox does not inject its default user/SSH-key block
on top.

**Why**: anyone sophisticated enough to write a cloud-init file can
paste their own SSH key into it. The "default + extra packages" use case
that motivated `--cloud-init-merge` is rare in practice, and merging
grows edge cases fast (list-merge vs replace for `runcmd`,
`write_files`, `packages`, etc.) for marginal value.

**Operational consequence**: a user who passes `--cloud-init` and forgets
to include an `ssh_authorized_keys` block will boot a VM they cannot
SSH into. The README's cloud-init section must lead with a complete
working example that includes SSH key injection, not a minimal
"packages: [htop]" snippet.

**Not adding later unless asked**: if a real user asks for merge
semantics with a concrete use case, we revisit. Until then, the surface
stays small.
