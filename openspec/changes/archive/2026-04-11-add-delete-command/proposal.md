## Why

Today the only way to remove a pmox-launched VM is to open the Proxmox web UI, shut the VM down, and click Remove. That defeats pmox's multipass-style pitch and makes iterating on templates and launches painful (every failed experiment becomes a browser round-trip). An earlier change, `list-info-lifecycle`, bundles delete together with list/info/start/stop/clone/resolver as one 76-task slice, which is too big to ship while the user is actively hitting this friction. This change carves the delete slice out so it can land on its own and immediately unblock the day-to-day loop.

## What Changes

- Add `pmox delete <name|vmid>` as a new top-level command.
- Refuse to destroy VMs that are not tagged `pmox`, unless `--force` is passed. The `pmox` tag is already applied by `launch-default` immediately after clone, so every pmox-launched VM is protected-by-default and every hand-managed VM is protected-from-accident.
- `--force` also switches the stop phase from graceful `shutdown` (ACPI) to hard `stop` (power-off), so a hung VM can still be destroyed.
- Treat "already gone" as success: if `GetStatus` returns `ErrNotFound`, print a note to stderr and exit 0. Re-running `pmox delete` after a Ctrl-C mid-destroy is safe.
- Add the minimum `pveclient` surface needed: `Shutdown`, `Stop`, `ClusterResources` (for name↔VMID↔node resolution and tag reads). `Delete` and `GetStatus` already exist.
- Add `internal/vm` with `Resolve(arg)` and `HasPMOXTag(tagsRaw)` — a small shared package the future `list`, `info`, `start`, `stop`, `clone` commands will reuse unchanged.
- Scope note: `list-info-lifecycle` will be rebased after this lands to drop the overlapping delete/resolver/pveclient pieces.

## Capabilities

### New Capabilities

- `delete-command`: the `pmox delete` CLI command, its flags, its tag-safety rules, and the stop-then-destroy sequencing behavior.

### Modified Capabilities

- `pveclient-core`: add `Shutdown`, `Stop`, and `ClusterResources` client methods. These are additive and do not change any existing requirement.

## Impact

- New code: `cmd/pmox/delete.go`, `internal/vm/resolve.go` (+ tests), `internal/pveclient/cluster.go` (+ tests), new methods in `internal/pveclient/vm.go`.
- `cmd/pmox/main.go`: register `newDeleteCmd()` alongside `newLaunchCmd()`.
- No change to `launch-default`, `configure-and-credstore`, `server-resolution`, or the `pmox` tag semantics.
- No config file changes, no new env vars, no breaking CLI changes.
- Downstream: shrinks the surface area of the pending `list-info-lifecycle` change (tracked as a follow-up rebase, not in this slice).
