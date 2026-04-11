## Context

pmox already has `launch`, but removing a launched VM still requires the Proxmox web UI: click the VM, Shutdown, then Remove (greyed out until the VM is stopped). This change adds a native `pmox delete <name|vmid>` so the create→destroy loop stays in the terminal.

There is an existing, unstarted change `list-info-lifecycle` that bundles delete with list/info/start/stop/clone as one 76-task slice. That slice is too big to ship while the user is actively hitting delete friction. This change carves delete out as its own slice so it can land independently. After it merges, `list-info-lifecycle` will be rebased to drop the overlapping pieces (resolver, tag helper, `Shutdown`/`Stop`/`ClusterResources`).

The `pmox` tag invariant from `launch-default` (every pmox-launched VM gets tagged immediately after clone, before any other config) is what makes the tag-based safety rule viable: if the tag is missing, pmox did not launch that VM and `delete` should refuse by default.

## Goals / Non-Goals

**Goals**
- Ship a working `pmox delete` command with tag-based safety and `--force` escape hatch.
- Introduce the minimum shared helpers (`internal/vm` resolver and tag check, `Shutdown`/`Stop`/`ClusterResources` client methods) so the same plumbing is reused unchanged by the later `list`/`info`/`start`/`stop`/`clone` slice.
- Be idempotent against Ctrl-C mid-destroy: re-running `pmox delete` on an already-gone VM should succeed quietly.

**Non-Goals**
- No `list`, `info`, `start`, `stop`, `clone` commands in this change. They remain in `list-info-lifecycle`.
- No confirmation prompt (`--yes`). The `pmox` tag check is the safety net. Adding a prompt would fight the tag check without adding real value (a user who typed `pmox delete web1` meant to destroy `web1`).
- No JSON output for this command. There is no structured data to emit — either the VM gets destroyed or an error is printed. The root `--output` flag continues to exist but this command ignores it.
- No cleanup goroutine for partial failures. Match tack's "leave state" principle: if the shutdown task succeeded but the destroy task failed, the VM stays on the cluster (stopped, still tagged `pmox`) and the next `pmox delete` picks up where we left off.

## Decisions

### Decision: Tag-based safety instead of a confirmation prompt
- **What**: Refuse to delete a VM that is not tagged `pmox`. No `--yes` / no interactive confirm.
- **Why**: The `pmox` tag is applied by `launch-default` immediately after clone, which means every pmox-launched VM is already protected from accidental `pmox delete <wrong-vm>` typos, and every hand-managed VM is protected from pmox entirely. A confirm prompt adds friction for the common case (deleting a VM you just launched) while not actually protecting against the dangerous case (typing the wrong name of a VM you also launched — same tag, confirm would still pass).
- **Alternatives considered**:
  - *Interactive `--yes` / prompt*: rejected. Breaks scripting, adds friction, doesn't prevent typos within the pmox-tagged set.
  - *Dry-run flag*: rejected. No dry-run value here — the entire command is two API calls and the error messages are enough to know what would happen.

### Decision: `--force` means both "skip tag check" AND "hard stop"
- **What**: A single `--force` flag controls both relaxations: the untagged-VM guard, and the choice of `Stop` vs `Shutdown`.
- **Why**: In practice both relaxations are needed together. You reach for `--force` when "normal delete didn't work" — either because the VM is hand-managed (no tag) or because the guest isn't responding to ACPI. Two separate flags (`--force-untagged` + `--hard`) would be accurate but pedantic, and in both cases the user's intent is the same: "I know what I'm doing, just destroy it."
- **Alternatives considered**:
  - *Two flags*: rejected as over-engineered for a tool at this stage.
  - *`--force` only bypasses the tag check, hard-stop is always used*: rejected because graceful shutdown is the right default — it lets the guest flush disks, close connections, etc.

### Decision: Already-gone VM is exit 0 with a note
- **What**: If `GetStatus` returns `ErrNotFound`, print `VM %q is already gone` to stderr and exit 0.
- **Why**: This command's contract is "the VM should not exist after this returns." If the VM already doesn't exist, the contract is already satisfied. Treating it as success also makes re-running `pmox delete` after a Ctrl-C safe and cheap. The stderr note preserves the observability ("something happened here") without failing scripts that loop over a list of VMs.
- **Alternatives considered**:
  - *Exit non-zero on not-found*: rejected. Forces every caller to special-case the not-found error.
  - *Silent success*: rejected. Hides the fact that the VM was gone before we arrived.

### Decision: Resolve the VM's node from `ClusterResources`, not from user input
- **What**: The command does not take a `--node` flag. It calls `ClusterResources(ctx, "vm")` and reads the node from there.
- **Why**: VMIDs are cluster-unique in PVE, so there is exactly one correct node for a given VMID. Making the user pass it would be busywork and a typo vector. The same `ClusterResources` call returns the tags field, so we fold the resolver call and the tag check into one round trip.
- **Alternatives considered**:
  - *Take a `--node` flag*: rejected. Redundant information, violates pmox's "keep flags minimal" house style.
  - *Assume a single default node*: rejected. pmox targets multi-node clusters.

### Decision: Shared `internal/vm` package now, even though only `delete` uses it
- **What**: Put `Resolve` and `HasPMOXTag` in `internal/vm/resolve.go` even though this change has only one caller.
- **Why**: The follow-up `list-info-lifecycle` slice needs the exact same helpers across five commands. Putting them in the right place now avoids a later refactor and makes the delete implementation a clean example for the next slice to copy.
- **Alternatives considered**:
  - *Inline into `cmd/pmox/delete.go`*: rejected. Would have to be extracted in the next slice, and the extracted version would look identical.

## Risks / Trade-offs

- **Risk**: `ClusterResources` tag field parsing differs between PVE versions (`;` vs `,` separator). Mitigated by `HasPMOXTag` handling both, with a test table covering the variants.
- **Risk**: A user with a hung guest tries `pmox delete` (graceful), the shutdown task times out, and they have no clear next step. Mitigated by `--force` doing hard-stop, and by the error message from `WaitTask` already being descriptive in `launch-default`.
- **Risk**: Overlap with `list-info-lifecycle` produces merge pain when that slice is eventually picked up. Mitigated by the scope note in the proposal: `list-info-lifecycle` will be rebased, not merged as-is.
- **Trade-off**: The tag check makes `pmox delete` refuse VMs that pre-date the `pmox` tag convention. Acceptable — `--force` is the documented escape hatch and the common case (VMs launched after this ships) is already tagged.

## Migration Plan

No migration. This is purely additive:
- No existing command's behavior changes.
- No config file keys added or removed.
- No pveclient methods changed (`Delete`, `GetStatus`, `Clone`, `Start` keep their existing signatures).
- The new `internal/vm` package has no existing callers to migrate.

After this change merges, update `openspec/changes/list-info-lifecycle/` to drop the delete requirement, the resolver requirement, and the overlapping pveclient tasks. That rebase is tracked as a follow-up, not part of this change's task list.

## Open Questions

None blocking. One thing to revisit after shipping: should `pmox launch --delete-on-failure` reuse the same destroy path? Probably yes, but that is a separate conversation and does not belong in this slice.
