## Why

`pmox mount` and `pmox umount` still demand an explicit `<name|vmid>:` prefix on every invocation, even though the existing `interactive-target-picker` capability already lets `shell`, `exec`, `delete`, `start`, `stop`, and `info` auto-resolve or prompt for the target. In the common case — one pmox VM on the cluster, or a user who just ran `pmox list` — typing the VM name again is friction, and there is no ergonomic way to tear down all mounts for the single VM you have running.

## What Changes

- `pmox mount` SHALL accept the destination as either `<name|vmid>:<remote_path>` (existing) **or** a bare `<remote_path>` (new). When the destination has no `<name|vmid>:` prefix, the command SHALL delegate VM resolution to the shared `interactive-target-picker` helper:
  - exactly one pmox VM → auto-select silently;
  - multiple pmox VMs + TTY → arrow-key picker;
  - multiple pmox VMs + non-TTY → existing missing-argument error;
  - zero pmox VMs → existing "no pmox VMs found" error.
- `pmox mount --help` SHALL be updated to document that the `<name|vmid>:` prefix is optional and to show a bare-path example.
- `pmox umount` SHALL accept being called with **no positional arguments at all**. In that case the command SHALL resolve the pmox VM set via the same helper:
  - exactly one pmox VM → stop **all** mounts for that VM (equivalent to today's `pmox umount --all <vm>`);
  - multiple pmox VMs + TTY → picker, then stop all mounts for the selected VM;
  - multiple pmox VMs + non-TTY → existing missing-argument error;
  - zero pmox VMs → existing "no pmox VMs found" error.
- `pmox umount --help` SHALL be updated to document the zero-arg form.
- No changes to `--force`, tag-check, exclude handling, PID-file format, or the rsync command line. Every existing invocation continues to work unchanged.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `mount-command`: `pmox mount` makes the `<name|vmid>:` prefix on the destination optional and delegates to the target picker when absent. `pmox umount` makes all positional arguments optional and, when called bare, resolves the VM via the picker and stops every mount for that VM.

## Impact

- Affected code: `cmd/pmox/mount.go` (argument parsing for both `mount` and `umount`, help text, and picker wiring), and — if needed — a thin call into the existing target-picker helper used by `shell`/`exec`/`delete`.
- Affected specs: delta on `mount-command` capturing the new optional-target behavior for both `mount` and `umount`. No changes to `interactive-target-picker` itself; this change just adopts the existing helper from two more call sites.
- No config migration. No breaking CLI changes — callers that pass `<name|vmid>:<path>` to `mount` or `<name|vmid>:<path>` / `--all <vm>` to `umount` see identical behavior.
- Dependencies: none new. Reuses the existing picker and the `ClusterResources` call already used by `pmox list`.
