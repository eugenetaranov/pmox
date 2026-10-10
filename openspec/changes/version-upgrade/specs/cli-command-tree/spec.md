## MODIFIED Requirements

### Requirement: Noun-verb command groups

pmox SHALL organize commands into singular noun groups:

| Group | Verbs |
|---|---|
| `vm` | `launch`, `clone`, `list`/`ls`, `info`, `start`, `stop`, `delete`/`rm`, `shell`, `exec`, `cp`, `sync`, `apply`, `ssh-config` |
| `template` | `create`, `list`/`ls` |
| `context` | `list`/`ls`, `add`, `use`, `current`, `rename`, `delete`/`rm` |
| `config` | `edit`, `path`, `cloud-init` |
| `mount` | `create`, `list`/`ls`, `delete`/`rm` |
| `key` | `publish`, `unpublish`, `show` |
| `access` | `grant`, `revoke`, `list`, `sync` |

`ls` and `rm` SHALL be the only verb aliases. `init`, `doctor`, `cleanup`, `version` and `completion` SHALL remain root commands. `version` SHALL keep printing the version when run alone, and SHALL have one verb, `upgrade` (see `self-upgrade`). The palette SHALL run `version` directly; `upgrade` is reached as `pmox version upgrade`.

#### Scenario: Canonical VM listing
- **WHEN** a user runs `pmox vm list`
- **THEN** pmox lists VMs exactly as `pmox list` did before this change

#### Scenario: Template creation under its noun
- **WHEN** a user runs `pmox template create`
- **THEN** the interactive template build starts

#### Scenario: Listing templates
- **WHEN** a user runs `pmox template list`
- **THEN** pmox lists the cluster's templates with VMID, name and node

#### Scenario: Version keeps printing
- **WHEN** a user runs `pmox version`
- **THEN** it prints `pmox version <version> (commit: …, built: …)` as before

#### Scenario: Version from the palette
- **WHEN** a user picks `version` in the bare `pmox` palette
- **THEN** pmox prints the version and build information, without opening a submenu

#### Scenario: Upgrade under version
- **WHEN** a user runs `pmox version upgrade --check`
- **THEN** pmox reports whether a newer release exists
