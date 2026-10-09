# cli-command-tree Specification

## Purpose
TBD - created by archiving change cli-noun-verb. Update Purpose after archive.
## Requirements
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

`ls` and `rm` SHALL be the only verb aliases. `init`, `doctor`, `cleanup`, `version` and `completion` SHALL remain root commands.

#### Scenario: Canonical VM listing
- **WHEN** a user runs `pmox vm list`
- **THEN** pmox lists VMs exactly as `pmox list` did before this change

#### Scenario: Template creation under its noun
- **WHEN** a user runs `pmox template create`
- **THEN** the interactive template build starts

#### Scenario: Listing templates
- **WHEN** a user runs `pmox template list`
- **THEN** pmox lists the cluster's templates with VMID, name and node

### Requirement: Top-level shortcuts with full parity

pmox SHALL provide these top-level shortcuts for their canonical commands:

- `launch`, `list`/`ls`, `info`, `start`, `stop`, `delete`/`rm`
- `shell`, `exec`, `cp`, `sync`, `apply`
- `mount`, and `umount` (for `mount rm`)

A shortcut SHALL accept the same flags and arguments as its canonical command. It SHALL produce the same output and exit codes.

#### Scenario: Shortcut and canonical are equivalent
- **WHEN** `pmox list --output json` and `pmox vm list --output json` run against the same cluster
- **THEN** both print identical JSON and exit with the same code

#### Scenario: Flag parity is enforced
- **WHEN** a flag is added to only one of a shortcut and its canonical command
- **THEN** the command-tree test fails

### Requirement: Mount as a noun

`pmox mount` SHALL be a group with `create`, `list` and `delete`/`rm`. `pmox mount <local> <vm>:<path>` SHALL still create a mount. `pmox mount list` SHALL show running background mounts with VM, remote path, local path and PID, and SHALL support `--output json`.

#### Scenario: Old mount shape still works
- **WHEN** a user runs `pmox mount ./src web1:/opt/app`
- **THEN** a background mount is created exactly as with `pmox mount create ./src web1:/opt/app`

#### Scenario: List running mounts
- **WHEN** two background mounts are running and the user runs `pmox mount list`
- **THEN** both are listed with their VM, paths and PID

### Requirement: Sectioned help

Root `--help` SHALL list commands under these sections:

- **Get started:** init, launch, shell
- **Common:** the remaining shortcuts
- **Resources:** the noun groups
- **Maintenance:** doctor, cleanup, version, completion

Each shortcut's summary SHALL name its canonical form. Deprecated forms SHALL NOT appear in help or shell completion.

#### Scenario: Help teaches the canonical form
- **WHEN** a user runs `pmox --help`
- **THEN** the `list` line shows that it is `vm list`

### Requirement: Interactive navigation of groups

On a terminal, bare `pmox` SHALL offer the root commands and each noun group. Choosing a group SHALL show that group's verbs. A bare noun such as `pmox vm` SHALL show the group's verbs. Without a terminal, or with `--no-input`, a bare noun SHALL print the group's help and exit with the user-input error code.

#### Scenario: Bare noun in a script
- **WHEN** `pmox vm` runs with stdin not a terminal
- **THEN** it prints the vm group's help and exits with code 2

### Requirement: Deprecated forms keep working

pmox SHALL keep these old forms working for at least two minor releases after this change:

- `create-template`
- `config get-contexts`, `use-context`, `current-context`, `rename-context`, `delete-context`
- `init --list`, `init --remove <url>`, `init --regen-cloud-init`
- top-level `ssh-config` and `clone`

Each SHALL print one line naming its replacement on stderr, never on stdout. Its stdout output and exit code SHALL be identical to the replacement's.

#### Scenario: Deprecated command still runs
- **WHEN** a user runs `pmox config use-context lab`
- **THEN** the current context becomes `lab`
- **AND** stderr shows one line pointing at `pmox context use`

#### Scenario: JSON stays clean
- **WHEN** a script runs `pmox config get-contexts --output json`
- **THEN** stdout contains only the same JSON `pmox context list --output json` prints

### Requirement: Canonical hints

Every hint pmox prints SHALL name commands by their canonical noun-verb form. This covers error hints, doctor remediation and next-step suggestions.

#### Scenario: Doctor remediation uses the new form
- **WHEN** doctor reports that no template is configured
- **THEN** its remediation suggests `pmox template create`

