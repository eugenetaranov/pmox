## ADDED Requirements

### Requirement: Cleanup categories

`pmox cleanup` SHALL collect removable pmox leftovers grouped into
categories: `snippet` (orphaned cloud-init snippets on the cluster),
`mount-record`, `log`, `known-host`, `cloud-init`, `tack-profile`,
`secret` (local artifacts), and `template` (pmox-generated templates).
It SHALL remain dry-run by default and remove only under `--apply`.

#### Scenario: Dry-run reports without removing

- **WHEN** `pmox cleanup` runs without `--apply`
- **THEN** it lists the items it would remove, grouped by category, and removes nothing

#### Scenario: Apply removes selected items

- **WHEN** `pmox cleanup --apply` runs
- **THEN** the selected items are removed and a summary is printed

### Requirement: Local config cleanup categories

`pmox cleanup` SHALL detect stale local pmox artifacts: `cloud-init`
files whose server is no longer configured, `tack-profile` entries for
servers/VMs that no longer exist, and file-backend `secret` entries for
servers no longer configured. Orphaned OS-keychain secrets are out of
scope (not enumerable) and are handled at server-removal time.

#### Scenario: Orphaned cloud-init file

- **WHEN** a `~/.config/pmox/cloud-init/<slug>.yaml` exists for a server not in config.yaml
- **THEN** it is offered under the `cloud-init` category

#### Scenario: Stale tack profile

- **WHEN** a remembered tack profile references a server/VMID that no longer exists
- **THEN** it is offered under the `tack-profile` category

#### Scenario: Orphaned file-backend secret

- **WHEN** the file secret backend is active and secrets.yaml has an entry for a server not in config.yaml
- **THEN** it is offered under the `secret` category

#### Scenario: Keychain secrets are not enumerated

- **WHEN** the OS keychain is the active backend
- **THEN** cleanup does not attempt to enumerate keychain entries and says so if relevant

### Requirement: Template cleanup is destructive and opt-in

`pmox cleanup` SHALL treat pmox-generated templates as a `template`
category that is destructive (it deletes VMs). It MUST NOT be selected by
default; it is included only when explicitly ticked in the interactive
checklist, or via `--include-templates`, or `--only template`. Templates
SHALL be identified conservatively by the create-template naming
convention (`-pmox-` in the name) together with the template flag and the
9000–9099 VMID range, and removed only under `--apply`.

#### Scenario: Templates are never removed by default

- **WHEN** `pmox cleanup --apply` runs with no template selection
- **THEN** no template is removed

#### Scenario: Templates removed only when opted in and applied

- **WHEN** `pmox cleanup --include-templates --apply` runs
- **THEN** matching pmox templates are removed

#### Scenario: A non-pmox template in range is not matched

- **WHEN** a template in the 9000–9099 range does not have a `-pmox-` name
- **THEN** it is not offered under the `template` category

### Requirement: Interactive category selection

On a terminal and when no selection flags are given, `pmox cleanup` SHALL
present a multi-select checklist of the categories that have items, with
non-destructive categories pre-checked and `template` unchecked, and act
only on the chosen categories.

#### Scenario: Checklist scopes the run

- **WHEN** the user unchecks a category in the checklist and confirms
- **THEN** items in that category are neither reported for removal nor removed

#### Scenario: Unchecking everything is a no-op

- **WHEN** the user unchecks all categories
- **THEN** cleanup removes nothing and exits without error

### Requirement: Non-interactive category selection flags

`pmox cleanup` SHALL support selecting categories without a terminal:
`--only <cats>` restricts to exactly those categories, `--skip <cats>`
removes categories from the default safe set, and `--include-templates`
adds the destructive template category. `--no-input` and `--output json`
SHALL skip the checklist and use the flag-resolved set (default: all
non-destructive categories). An unknown category key SHALL be an error.

#### Scenario: only restricts categories

- **WHEN** `pmox cleanup --only snippet,log` runs
- **THEN** only snippet and log items are considered

#### Scenario: skip removes from the default set

- **WHEN** `pmox cleanup --skip known-host` runs
- **THEN** every default category except known-host is considered

#### Scenario: no-input never prompts

- **WHEN** `pmox cleanup --no-input` runs without selection flags
- **THEN** no checklist is shown and the safe default set is used

#### Scenario: Unknown category errors

- **WHEN** `pmox cleanup --only bogus` runs
- **THEN** cleanup exits with a user-input error naming the invalid category
