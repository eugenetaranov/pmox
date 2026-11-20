# Proposal

## Why

pmox grew command by command, and the same idea is now spelled several ways: `--force` means "power off" on `stop` but "ignore the tag check" on `delete`; `--server` and `--context` do the same job; `resize` is three subcommands with different flags; `apply` hides three management actions behind flags that "do X and exit". This change makes each idea have one spelling, with every old form kept working through the usual deprecation window.

Depends on `default-vm` and `vm-completion-and-restart` being applied first: the command table here includes `vm use` and `vm restart`.

## What Changes

- **`--hard` means power-off everywhere; `--force` only bypasses the pmox tag check.** `stop --hard` is added; `stop --force` keeps working as a deprecated spelling of it.
- **`--context` is the one way to target a server.** It accepts a name or a URL. `--server` and `PMOX_SERVER` keep working, hidden, with a note.
- **`pmox resize <vm> --cpu N --mem GiB --disk +N|N`** replaces `vm resize cpu|ram|disk`, using `launch`'s flag names, and becomes a top-level shortcut. The three subcommands stay as deprecated forms. `launch` and `clone` accept fractional `--mem` (`1.5`), as resize does.
- **`pmox info [name|vmid]`**: with a VM it shows that VM (same as `vm info`); alone it keeps showing the Proxmox host.
- **`pmox playbook init|default|update|list`** replaces `apply --init`, `--default`, `--update`. `apply`'s second argument is called a playbook everywhere (usage said `profile`).
- **`pmox context edit`** replaces `config edit`.
- **`cleanup --yes`** is accepted as `--apply`; `--include-templates` / `--include-vms` are deprecated in favour of `--include`.
- **`router route delete`** (alias `rm`) is the canonical name, like every other group; **`access revoke --from`** replaces `--to` on revoke.
- **One yes/no prompt.** Every confirmation is the `[y/N]` line; `resize` and `context delete` stop using the button picker.
- **The palette filter searches the whole command tree**, so typing `resize` at the root finds `vm resize`.
- **`remount` leaves root help** (it keeps working); root help lists `mount` and `umount`.
- **Help text**: every command gets an Examples section, changelog phrasing and API-endpoint detail are removed, stale `configure` wording is fixed, and subcommand pages stop repeating the global flags.
- No form is removed in this change. **BREAKING** only in the sense that deprecated forms now print a one-line note on stderr.

## Capabilities

### New Capabilities
- `vm-resize`: changing a VM's cores, memory and disk with one command.
- `playbook-management`: setting up, choosing, listing and updating the tack playbooks `pmox apply` runs.

### Modified Capabilities
- `cli-command-tree`: command table, shortcuts and deprecated forms updated; palette search and help-text rules added.
- `list-info-lifecycle`: `stop --hard`; root `info` with and without a VM.
- `server-resolution`: `--context` takes a name or URL; `--server` is deprecated.
- `cleanup`: `--yes` and the `--include` shorthands.
- `interactive-confirmation`: one confirmation style for all commands.
- `launch-default`: fractional `--mem`.
- `vm-access-registry`: `revoke --from`.
- `overlay-router`: `route delete` naming.

## Impact

- `cmd/pmox/tree.go`, `main.go`, `stop.go`, `resize.go`, `launch.go`, `clone.go`, `hostinfo.go`, `info.go`, `apply.go`, `playbooks.go`, `config.go`, `cleanup.go`, `router.go`, `access.go`, `client.go` / `internal/server`, `internal/tui/palette`, and the Long/Example text of every command.
- README command tables, the "Renamed commands" table, `docs/`, `llms.txt`.
- Scripts using deprecated forms keep working and see one stderr line. Removal is a later change, after at least two minor releases.
