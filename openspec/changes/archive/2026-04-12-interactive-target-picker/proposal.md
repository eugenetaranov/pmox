## Why

Every pmox command that operates on a single VM (`shell`, `exec`, `start`, `stop`, `info`, `delete`, `clone`, `mount`, `umount`, and the remote side of `cp`/`sync`) requires the user to type the VM name or vmid up front, and errors out if it is missing. In the common case there is only one pmox-tagged VM on the cluster, or the user is already looking at a list from `pmox list`; making them retype a name is friction. When there are several VMs, forcing the user to `pmox list` first, memorize a name, then re-invoke the command is worse than just offering a picker.

## What Changes

- When a VM-target command is invoked without a positional target argument, resolve the list of pmox-tagged VMs via the existing cluster query and then:
  - If exactly one pmox VM exists, silently use it as the target (no prompt).
  - If two or more exist, show an interactive picker listing name / vmid / node / status / IP (same columns as `pmox list`) and let the user arrow-select one.
  - If zero exist, print a friendly "no pmox VMs found" message and exit non-zero.
- The picker SHALL only activate when stdin and stderr are both TTYs. In non-interactive contexts (pipes, CI, scripts) a missing target SHALL continue to error with the existing usage message, preserving scriptability.
- Commands that take *two* positional arguments where one is the target (`clone <src> <dst>`, `cp <src> <dst>`, `sync <src> <dst>`, `mount <local> <name:remote>`) SHALL trigger the picker only when the user gives no arguments at all AND the remaining non-target argument can be inferred or prompted for; otherwise the existing usage error wins. This keeps scope bounded: the primary target of this change is the single-positional commands.
- No changes to flags, config, or the `--force` / tag-check behavior. The picker lists the same set of VMs the commands would accept today (pmox-tagged only, unless `--force` was passed, in which case all VMs are shown).

## Capabilities

### New Capabilities
- `interactive-target-picker`: a shared interactive VM-picker helper used by every command that takes a `<name|vmid>` positional argument, plus the TTY-detection and auto-select-single rules that govern when it activates.

### Modified Capabilities
- `ssh-shell-exec`: `pmox shell` and `pmox exec` SHALL make the `<name|vmid>` argument optional and delegate to the picker when omitted.
- `delete-command`: `pmox delete` SHALL make the `<name|vmid>` argument optional and delegate to the picker when omitted; the existing y/N confirmation still applies to the picked VM.

## Impact

- Affected code: `cmd/pmox/ssh.go`, `cmd/pmox/delete.go`, `cmd/pmox/start.go`, `cmd/pmox/stop.go`, `cmd/pmox/info.go`, and a new helper package (likely `internal/targetpick` or inside `internal/vm`) that owns the picker UI and TTY detection.
- Affected specs: new `interactive-target-picker` spec; delta specs for `ssh-shell-exec` and `delete-command`. `start`/`stop`/`info` are not yet captured by dedicated spec files, so they pick up the new behavior transitively via the new spec.
- Dependencies: reuse the existing TUI primitives in `internal/tui` if they already provide a selectable list; otherwise add a small dependency-free arrow-key picker (stdlib + `golang.org/x/term` which is already transitively available via `golang.org/x/crypto/ssh`).
- No config migration, no breaking CLI changes — every existing invocation with an explicit target continues to work unchanged.
