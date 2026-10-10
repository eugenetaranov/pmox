## Why

Several commands wait on the cluster with nothing on screen. Picking
`start` in the `pmox` palette leaves a blinking cursor while the VM
boots and the guest agent comes up, so it looks hung and gets
interrupted. Only `launch`, `clone`, `delete` and `template create`
show progress today, each with its own spinner wiring.

Commands also treat "already done" as failure. `pmox start` on a
running VM prints the raw Proxmox task error
(`VM 100 already running`) and exits non-zero, though the user got
what they wanted.

## What Changes

- A shared delayed spinner (`internal/tui.Spinner`): drawn on stderr
  only on a terminal, only after ~250 ms, its label updates in place,
  and it ends in a "✓" result line or is cleared before an error or
  prompt. It is a no-op when off or nil.
- Every command that waits on the API, a task, the guest agent, node
  SSH, the access registry or a download shows it, including the VM
  picker's "Loading VMs…" step. The existing launch/delete/template
  spinners use the same implementation, so they get the delay too.
- "Already in the desired state" is success (exit 0, "✓" line):
  start on a running VM, stop on a stopped VM, and the matching cases
  in delete, key unpublish, access revoke and umount.
- A new `cli-progress-feedback` spec so later commands keep to this.

## Impact

- `internal/tui/spinner.go` (new), `cmd/pmox/spinner.go` (built on it),
  and most command files under `cmd/pmox`, plus `internal/vm/pick.go`.
- Output on a terminal changes: results gain a "✓" prefix where a
  spinner ran. Non-terminal output, JSON output and exit codes are
  unchanged, except that the "already" cases now exit 0.
