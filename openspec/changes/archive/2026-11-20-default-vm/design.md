# Design

## Context

All single-target resolution goes through `vm.Pick` (`internal/vm/pick.go`); `<vm>:<path>` targets go through `cmd/pmox/target_prompt.go` and `internal/tui/target`. Per-server settings live in `config.Server`. See proposal.md for motivation.

## Goals / Non-Goals

**Goals:** one place decides whether the default applies; destructive commands cannot pick it up by accident.

**Non-Goals:** a default per directory or per shell session; an environment variable for the default; remembering the last-used VM automatically.

## Decisions

### Store the name, per server
`default_vm: web1` on the server entry. A name survives the rebuild-under-the-same-name workflow, where the VMID changes; `pmox vm use 104` resolves the VMID and stores the name.
- *Alternative: store the VMID.* Rejected for the reason above.
- *Alternative: a state file next to mount records.* Rejected: this is a user setting, belongs with `current_context`, and should follow the config when it is copied to another machine.
- If two VMs share the stored name, resolution reports the usual ambiguous-name error rather than guessing.

### Opt-in at the call site
`vm.Pick` takes an options value (`UseDefault bool`, `Default string`, `ForcePick bool`). Commands in the "use the default" list pass `UseDefault`; `PickMulti` has no such option at all, so `stop` and `delete` cannot opt in by mistake. A table test walks the command tree and asserts exactly the listed commands register `--pick`.

### Stale default
The helper already fetches the VM set, so "default not in set" costs nothing. On that path it clears the field with the existing atomic config write and prints one stderr line. An API error returns before this check, so an unreachable cluster never clears anything.

### Say which VM was chosen
One stderr line (`→ web1 (default VM)`) when the default decided the target, so a surprising target is visible, in the same place the auto-selected-single-VM path reports. Not printed with `--output json` on stdout; stderr only.

### Marker
`list` gets a narrow leading marker column rather than a `DEFAULT` column, matching how little room the table has; `template list` keeps its own column.

## Risks / Trade-offs

- [A command runs on the default when the user expected a picker] → the stderr line names the target; destructive commands are excluded; `--pick` exists.
- [Config written by a read-mostly command when clearing a stale default] → uses the same atomic write as `context use`; failure to write is reported and does not fail the command.
- [`--pick` flag name collides later] → it is added only where the default applies.
