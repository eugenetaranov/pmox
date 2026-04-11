## Why

Slices 1–4 shipped the binary skeleton, credential store, server
resolver, and the full Proxmox HTTP client. None of that is usable
yet — there's no command that actually creates a VM. `launch-default`
is the first slice with real end-user value: a single
`pmox launch <name>` call that clones a template, resizes its disk,
waits for the guest agent, and prints an IP the user can SSH to.

This is the slice where all five cross-slice decisions (D-T1..D-T5)
finally cash out: tagging before resize, qemu-guest-agent-only IP
discovery, `-v` server-resolution log line, full-replace cloud-init,
and no auto-rollback on partial failure.

## What Changes

- Add `pmox launch <name>` subcommand. Happy-path only — built-in
  cloud-init, no `--cloud-init` file yet (that's slice 7).
- Orchestrate the 9-step launch state machine from D-T1:
  `nextid → clone → tag → resize → config → start → wait-IP → wait-SSH → done`.
- Built-in cloud-init user-data: default `ciuser` (`pmox`), injects
  the configured SSH public key, enables `qemu-guest-agent`, sets
  `password_auth: false`.
- Flags: `--cpu N`, `--mem MB`, `--disk NG`, `--template <id|name>`,
  `--storage <id>`, `--node <name>`, `--bridge <name>`, `--wait <dur>`,
  `--no-wait-ssh`, `--user <name>`, `--ssh-key <path>`. Each flag
  falls back to the configured server default; unset values use
  built-in defaults (2 CPU, 2048 MB, 20G, etc.).
- Tag the VM with `pmox` immediately after clone, **before** resize,
  so orphaned VMs from a failed launch are cleanable via
  `pmox delete` (slice 6) without `--force`. Per D-T1.
- IP discovery via `qemu-guest-agent` only — no DHCP lease fallback.
  Polls `AgentNetwork` until an interface reports a usable IPv4 per
  D-T3's picking heuristic. On timeout, fails with the exact message
  from D-T3.
- SSH wait: after the agent reports an IP, open a TCP dial to `:22`
  and complete an SSH handshake (no command, no shell) to confirm
  the VM is actually reachable. `--no-wait-ssh` skips this.
- Partial-failure behavior: **no auto-rollback**. Any step after
  `clone` that fails leaves the VM on the cluster tagged `pmox`.
  The error message names the VMID and suggests `pmox delete <vmid>`.
- When `-v` is set, emit the server-resolution log line from D-T4
  before the first API call.
- Happy-path integration test using a fake PVE server
  (`httptest.Server` + canned JSON fixtures) that walks the full
  9-step state machine. No real PVE cluster is required for CI.

## Capabilities

### New Capabilities
- `launch-default`: the `pmox launch` command plus the orchestration
  state machine that turns a template ID, size knobs, and an SSH
  key into a running, reachable VM. Owns the 9-step sequence, the
  built-in cloud-init user-data, the IP-picking heuristic, and the
  SSH-wait helper.

### Modified Capabilities
- None. `pveclient-core`, `server-resolution`, and
  `configure-and-credstore` are consumed as-is.

## Impact

- **New files**: `cmd/pmox/launch.go` (Cobra wiring big enough to
  split out, matching tack's `vault.go`/`export.go` precedent),
  `internal/launch/launch.go` (state machine), `internal/launch/cloudinit.go`
  (built-in user-data template), `internal/launch/ip.go` (agent
  polling + IP picker from D-T3), `internal/launch/ssh.go` (SSH
  handshake wait). Test siblings for each.
- **Modified files**: `cmd/pmox/main.go` — register the `launch`
  subcommand on the root Cobra command.
- **New dependency**: `golang.org/x/crypto/ssh` for the SSH wait
  helper. No other new modules.
- **Read-only consumers**: `internal/config`, `internal/credstore`,
  `internal/server`, `internal/pveclient`, `internal/tui`.
- **No schema changes**: config file format is unchanged.
- **Cross-slice contract**: slice 6 (`list-info-lifecycle`) will
  consume the `pmox` tag convention set here — the tag is how
  pmox distinguishes its own VMs from hand-managed ones on the
  cluster.
