# Design

## Context

`cmd/pmox/tree.go` already has the two tools this change leans on: `deprecated(cmd, replacement)` for whole commands (hidden, one stderr note, same behaviour) and a parity test between shortcuts and canonical commands. There is no equivalent yet for a deprecated *flag*. See proposal.md for the list of renames.

## Goals / Non-Goals

**Goals:** every rename is an alias plus a note, never a behaviour change; the rules ("`--hard` means power-off", "every command has examples") are enforced by command-tree tests so they don't drift again.

**Non-Goals:** removing any old form; changing the `mount <local> <target>` positional shape; a VM-first home screen; scriptable `template create` — each needs its own change.

## Decisions

### A `deprecatedFlag` helper beside `deprecated`
`deprecatedFlag(cmd, old, new)` hides the old flag and, in a `PreRun` wrapper, prints `note: '--old' is deprecated — use '--new'` to stderr when the old flag was set, then copies its value to the new flag's variable. Cobra's own `MarkDeprecated` writes to the output writer, which is stdout for some commands — the same reason `deprecated` doesn't use cobra's command deprecation. Used for `stop --force`, `--server`, `cleanup --include-templates/--include-vms`, `access revoke --to`, `apply --init/--default/--update`.

### `--server` folds into `--context`
The resolver already takes both values. `--context` is passed through the same canonicalization `--server` uses, so a URL or bare host matches a configured server; a name is tried first. `PMOX_SERVER` is read where it is today and adds the note. Conflict (both set, different servers) is a user-input error rather than a precedence rule nobody would remember.

### `resize` as flags
One `RunE` builds a single config update for cores and memory, validates the disk request first (so a refused shrink changes nothing), applies the config update, grows the disk, then restarts at most once. The three old subcommands become `deprecated` commands that translate their positional value into the flag and call the same function.
- *Alternative: keep subcommands and add the flag form beside them.* Rejected: two documented ways is what this change is removing.
- `--mem` becomes a float-GiB parser shared by `launch`, `clone` and `resize`; it stores MiB. The JSON output of `launch` keeps reporting MiB, so no consumer changes.

### Root `info`
`info` at the root takes `[name|vmid]`; with an argument it calls the `vm info` run function, without one the host report. It is not built from the `vm info` constructor, so it is excluded from the parity test by name, with the bare-vs-argument behaviour covered by its own tests. The `--force`, `-u`, `-i` flags of `vm info` are registered on it so the argument form is at parity.

### `playbook` group instead of `apply init`
Subcommands under `apply` would collide with `apply`'s first positional — a VM named `init` or `update` — the same wart `mount` has with directory names. A noun group has no such collision and matches `template`. The run functions already exist in `playbooks.go` behind the flags; the group calls them directly.

### Confirmation
`resize` and `context delete` switch from `tui.Confirm` (huh buttons) to the `Confirmer` used by `delete`, `cleanup`, `doctor` and `router`. `tui.Confirm` stays for the init and access wizards, which are full-screen flows where a typed line would break the frame. `context delete` gains `--yes`.

### Palette search
On a non-empty filter the palette asks the resolver for a flattened list of every runnable command under the current path (path joined with spaces as the key), ranks own-level prefix matches first, then deeper prefix matches, then substring matches. Root shortcuts are dropped from the flattened list when their canonical command is present (using the existing canonical annotation), so each command appears once — shown under its shortcut name at the root, with the canonical path as the hint.

### Help text
- A custom usage template drops the `Global Flags:` block on non-root commands and prints `Global flags: see 'pmox --help'`.
- Examples move from `Long` into cobra's `Example` field.
- Tests walk the tree: every runnable, non-hidden command has `Example`; no `Long`/`Short` contains a banned phrase (`as before`, `same as always`, `exactly as`, `configure offers`, `/status/`, `api2/json`).
- The prose rewrite itself is manual, command by command; the tests only stop regressions.

## Risks / Trade-offs

- [A wrapper or alias in someone's shell uses an old form] → it keeps working; the note goes to stderr only, so captured stdout and JSON are unchanged.
- [`stop --force` users expect tag-bypass semantics later] → `stop` has no tag check today; if one is added, `--force` will mean that only after the deprecated spelling is removed.
- [The help rewrite loses a detail someone relied on] → long-form detail that is cut from `--help` moves to README/docs, not deleted.
- [This is a large change] → the task groups are independent and each lands its own tests and docs; they can be merged one group at a time.

## Migration Plan

1. Land after `default-vm` and `vm-completion-and-restart`.
2. Ship with the README "Renamed commands" table extended by every row in the deprecated list.
3. A later change removes the deprecated forms, no sooner than two minor releases on.
