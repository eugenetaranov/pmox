## Why

After slice 5 ships, users can create VMs but have no pmox-native
way to see what they created, inspect individual VMs, start/stop
them, destroy them, or duplicate one. The PVE web UI works, but
pmox's pitch is a multipass-style CLI — users shouldn't have to
context-switch to the browser for day-to-day VM operations.

This slice adds every non-creation lifecycle command: `list`,
`info`, `start`, `stop`, `delete`, `clone`. All of them are thin
orchestrations over `pveclient-core` and the `pmox` tag set by
`launch-default`, so they're small individually but need to ship
together for the tool to feel complete.

## What Changes

- Add `pmox list` — enumerate VMs on the resolved cluster tagged
  `pmox`, printing a table of `NAME | VMID | NODE | STATUS | IP`.
  IP is populated from `AgentNetwork` when the VM is running,
  blank otherwise. `--all` drops the tag filter and shows every
  VM on the cluster. `--output json` emits a JSON array instead
  of the table (the root `--output` flag is already registered
  in slice 1).
- Add `pmox info <name|vmid>` — print a detailed view of one VM:
  config (cpu/mem/disk/template source), status, IP addresses
  from the guest agent, uptime, tags. `--output json` prints the
  same fields as a JSON object.
- Add `pmox start <name|vmid>` — POST `/status/start`, wait for
  task, optionally wait for the guest agent to report an IP
  (same poll loop as `launch-default`). `--no-wait` returns as
  soon as the Start task finishes.
- Add `pmox stop <name|vmid>` — POST `/status/shutdown` (graceful,
  ACPI). `--force` sends `/status/stop` (hard power-off) instead.
  Waits for the task by default; `--no-wait` short-circuits.
- Add `pmox delete <name|vmid>` — stop the VM if running (graceful
  unless `--force`), then `DELETE /nodes/{node}/qemu/{vmid}`, then
  wait for the destroy task. Refuses to delete a VM that is not
  tagged `pmox` unless `--force` is passed (the tag-first-then-
  resize guarantee from D-T1 means every VM launched by pmox is
  tagged, so untagged VMs are assumed to be hand-managed).
- Add `pmox clone <source-name|vmid> <new-name>` — a thin wrapper
  that re-runs the relevant subset of the slice-5 launch state
  machine starting from `Clone` with the source VMID as the
  template. Inherits CPU/mem/disk from flags or from the source
  VM's config. Applies the `pmox` tag to the clone.
- Name↔VMID resolution: every command accepts either the VM name
  (e.g. `web1`) or the numeric VMID (e.g. `104`). Name resolution
  is a single pass over `GET /cluster/resources?type=vm` filtered
  to `name==arg`. Duplicate names are an error (`multiple VMs
  named "web1": vmids 104, 107 — pass the VMID instead`).
- Interrupt handling for `delete`: if the user Ctrl-Cs between
  the stop call and the destroy call, the partially-deleted VM
  stays on the cluster (still tagged `pmox`) and the error message
  suggests re-running `pmox delete`. No cleanup goroutine — match
  tack's "leave state" principle. This addresses one of the parked
  threads from ROADMAP.md.

## Capabilities

### New Capabilities
- `list-info-lifecycle`: the five non-creation lifecycle commands
  (`list`, `info`, `start`, `stop`, `delete`, `clone`), plus the
  shared name↔VMID resolver and the shared "confirm it's a pmox
  VM" gate.

### Modified Capabilities
- `pveclient-core`: adds one new endpoint — `ClusterResources(ctx, typeFilter string) ([]Resource, error)` hitting
  `GET /cluster/resources?type=vm`. Needed for name resolution
  and for `list`. Also adds `Shutdown(ctx, node, vmid)` and
  `Stop(ctx, node, vmid)` for the graceful-vs-force distinction.

## Impact

- **New files**: `cmd/pmox/list.go`, `cmd/pmox/info.go`,
  `cmd/pmox/start.go`, `cmd/pmox/stop.go`, `cmd/pmox/delete.go`,
  `cmd/pmox/clone.go` (all thin Cobra wiring), plus
  `internal/vm/resolve.go` (name↔VMID resolver shared across
  commands), `internal/vm/table.go` (the `list` table renderer),
  `internal/vm/info.go` (the `info` aggregator). Test siblings for
  each.
- **Modified files**: `internal/pveclient/` gains `cluster.go`
  (`ClusterResources`), and `vm.go` gains `Shutdown` and `Stop`.
  `cmd/pmox/main.go` registers all six new subcommands.
- **Breaking?**: no. Only adds commands and client methods.
- **Dependencies**: no new Go modules.
- **Cross-slice contract**: slice 7 (`cloud-init-custom`) will
  reuse the `internal/vm.Resolve` helper to let `--cloud-init`
  work alongside name-based targeting. Slice 9's README will
  document the full command table that this slice completes.
