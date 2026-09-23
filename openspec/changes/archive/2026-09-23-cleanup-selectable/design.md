## Context

`pmox cleanup` (`cmd/pmox/cleanup.go`) collects `cleanupItem`s across four
categories — `snippet` (remote), `mount-record`, `log`, `known-host`
(local) — and either reports them (dry-run, default) or removes them
(`--apply`). It scans every configured context and never touches VMs.

pmox's wider footprint also includes: per-server cloud-init files
(`config.CloudInitDir()/<slug>.yaml`), tack profile state
(`~/.local/state/pmox/tack/profiles.json`), file-backend secrets
(`secrets.yaml`), and templates built by `create-template` (named
`ubuntu-<rel>-pmox-<vmid>`, `template=1`, VMID 9000–9099).

## Goals / Non-Goals

**Goals:**
- Cover stale local config artifacts (cloud-init, tack profiles, secrets).
- Optionally remove pmox-generated templates, safely.
- Let the user choose categories (interactive checklist + flags).

**Non-Goals:**
- Enumerating/pruning orphaned *keychain* secrets — the OS APIs can't list
  entries; that stays at removal time (`configure --remove`).
- Removing non-pmox templates or any running/non-template VM.
- Changing dry-run-by-default or `--apply` semantics.

## Decisions

### 1. Category model
Each `cleanupItem` keeps its `Category`. Categories carry metadata:
`key`, human title, `destructive bool`, `local bool`. The set:

| key | scope | destructive | default-selected |
|-----|-------|-------------|------------------|
| `snippet` | remote | no | yes |
| `mount-record` | local | no | yes |
| `log` | local | no | yes |
| `known-host` | local | no | yes |
| `cloud-init` | local | no | yes |
| `tack-profile` | local | no | yes |
| `secret` | local | no | yes |
| `template` | remote | **yes** | **no** |

### 2. New collectors
- `cloud-init`: list `config.CloudInitDir()`; for each `<slug>.yaml`, if no
  configured server maps to that slug, flag it. (Slug derives from the URL
  the same way `CloudInitPath` does.)
- `tack-profile`: read the tack profile store; for each `server#vmid` key,
  if the server is still configured, check the VM exists via the already
  gathered per-context VM id set; flag entries whose server is gone or
  whose VMID no longer exists.
- `secret`: only when the active secret backend is the file store — read
  `secrets.yaml`; flag URL entries not present in `config.yaml`. Keychain
  is skipped (documented; not enumerable).
- `template`: per context, from the VM listing, flag resources where
  `template==1` AND name matches `ubuntu-…-pmox-…` (or contains `-pmox-`)
  AND VMID in 9000–9099. Removal calls the VM delete/destroy endpoint.

### 3. Selection resolution
Compute the *available* categories (those with ≥1 item), then the
*selected* set:
1. `--only a,b` → exactly those (intersected with available); unknown keys
   error.
2. else start from the default-selected set (all non-destructive
   available); `--skip a,b` removes; `--include-templates` adds
   `template`.
3. Interactive override: if a TTY and none of `--only`/`--skip`/
   `--include-templates`/`--no-input`/`--output json` is in play, show a
   multi-select pre-checked with the default set (template unchecked); the
   result replaces the selected set. Unchecking all = nothing to do.

`template` is only ever in the selected set via an explicit tick,
`--include-templates`, or `--only template` — never by default.

### 4. Template safety
- Never default-selected (Decision 1/3).
- Even when selected, deletion only happens under `--apply` (dry-run just
  reports), same as every category.
- Identified conservatively by the pmox naming convention *and* template
  flag *and* VMID range, so a user's own template in that range without
  the `-pmox-` name is never matched.

### 5. Non-interactive & JSON
`--no-input` and `--output json` skip the checklist and use the
flag-resolved set (default = safe categories). Category keys in
`--only`/`--skip` are validated; the JSON report already lists each item's
`category`, so selection is transparent.

### 6. TUI multi-select with defaults
Extend `internal/tui` with a multi-select that honors per-option
pre-checked state (huh `Option.Selected(true)`), so the checklist can
arrive with the safe set ticked and `template` unticked. The interactive
call is behind a function seam so tests drive selection without a TTY.

## Risks / Trade-offs

- **Deleting a template is destructive and irreversible.** Mitigations:
  opt-in, never default, conservative identification, `--apply`-gated,
  shown under a clearly labeled "destructive" heading.
- **Slug↔server mapping for cloud-init orphans** must match how files are
  written, or a live server's file could be flagged. Mitigation: derive
  the slug via the same code path as `CloudInitPath`; unit-test round-trip.
- **secret pruning removes credentials.** Only targets URLs absent from
  `config.yaml` (already unreachable), file backend only.
- **tack-profile VM-existence check** depends on per-context VM listing;
  if a context can't be reached, skip its profile entries (don't guess).
