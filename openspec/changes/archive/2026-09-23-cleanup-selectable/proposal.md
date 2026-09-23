## Why

`pmox cleanup` reclaims four kinds of leftover today (orphaned snippets,
dead mount records, orphaned logs, stale known_hosts pins) in an
all-or-nothing sweep. Two gaps: it ignores local pmox config artifacts
that go stale (cloud-init files, tack profiles, file-backend secrets) and
the Ubuntu templates `pmox create-template` builds; and it offers no way
to choose *which* categories to act on. Users want a selectable,
openspec-style checklist and coverage of the rest of pmox's footprint.

## What Changes

- **New local categories**:
  - `cloud-init` — `~/.config/pmox/cloud-init/<slug>.yaml` files whose
    server is no longer in `config.yaml`.
  - `tack-profile` — remembered tack profiles
    (`~/.local/state/pmox/tack/`) for VMs that no longer exist.
  - `secret` — entries in the file-backend `secrets.yaml` for servers no
    longer in `config.yaml`. (Keychain entries cannot be enumerated by
    the OS APIs, so orphaned *keychain* secrets stay handled at removal
    time by `configure --remove` / `config delete-context`; documented.)
- **New remote category `template`** (**destructive — deletes VMs**):
  pmox-generated templates, identified by the `create-template` naming
  convention (`ubuntu-<rel>-pmox-<vmid>`, `template=1`) in the 9000–9099
  VMID range. It breaks the old "cleanup never removes VMs" rule, so it is
  **opt-in and never selected by default**: it must be explicitly ticked
  in the interactive checklist or enabled with `--include-templates`, and
  only ever removed with `--apply`.
- **Selectable categories (openspec-style)**: on a TTY, `pmox cleanup`
  presents a multi-select checklist of the categories that actually have
  items — non-destructive ones pre-checked, `template` unchecked. The
  selection scopes what is reported/removed.
- **Non-interactive selection flags**: `--only <cats>` (exactly these),
  `--skip <cats>` (default set minus these), `--include-templates` (add
  the destructive template category). `--no-input` / `--output json`
  never show the checklist and default to the safe set.
- Dry-run-by-default and `--apply` semantics are unchanged; `--apply`
  still gates all deletion, including selected templates.

## Capabilities

### New Capabilities

- `cleanup` — the `pmox cleanup` command (no capability spec exists yet):
  its category model, the new local `cloud-init`/`tack-profile`/`secret`
  categories, the destructive opt-in `template` category, and interactive
  + flag-based category selection. (Existing snippet/mount/log/known-host
  behavior is described here too so the capability is self-contained.)

## Impact

- `cmd/pmox/cleanup.go` — category model, selection (interactive
  checklist + `--only`/`--skip`/`--include-templates`), new collectors.
- `internal/tui` — multi-select that honors pre-checked defaults.
- Reuses `config.CloudInitDir`, `tackStateDir`, the file-backend secrets
  path, and `pveclient` template listing/delete.
- Docs: README + llms.txt cleanup section.
- Tests: category collection, selection resolution, template
  identification, template safety (never default/without --apply).
