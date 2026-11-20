# Spec Delta

## MODIFIED Requirements

### Requirement: Noun-verb command groups

pmox SHALL organize commands into singular noun groups:

| Group | Verbs |
|---|---|
| `vm` | `launch`, `clone`, `list`/`ls`, `info`, `use`, `start`, `stop`, `restart`, `resize`, `delete`/`rm`, `shell`, `exec`, `cp`, `sync`, `apply`, `ssh-config` |
| `template` | `create`, `list`/`ls` |
| `context` | `list`/`ls`, `add`, `use`, `current`, `edit`, `rename`, `delete`/`rm` |
| `config` | `path`, `cloud-init` |
| `mount` | `create`, `list`/`ls`, `restart`, `autostart`, `delete`/`rm` |
| `playbook` | `init`, `list`/`ls`, `default`, `update` |
| `key` | `list`/`ls`, `publish`, `unpublish`, `show` |
| `access` | `setup`, `grant`, `revoke`, `show`, `sync` |
| `router` | `create`, `login`, `status`, `delete`/`rm`, `route` (`add`, `list`/`ls`, `delete`/`rm`) |
| `yardo` | `push`, `apply`, `verify`, `gitkey` |

`ls` and `rm` SHALL be the only verb aliases, and SHALL always alias `list` and `delete`. `init`, `doctor`, `cleanup`, `version` and `completion` SHALL remain root commands. `version` SHALL keep printing the version when run alone, and SHALL have one verb, `upgrade` (see `self-upgrade`). The palette SHALL run `version` directly; `upgrade` is reached as `pmox version upgrade`.

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

#### Scenario: Router under its noun
- **WHEN** a user runs `pmox router status`
- **THEN** pmox reports the overlay router (see `overlay-router`)

#### Scenario: Editing a server under context
- **WHEN** a user runs `pmox context edit prod`
- **THEN** the review screen for `prod` opens, as `pmox config edit prod` did

#### Scenario: Aliases are uniform
- **WHEN** a user runs `pmox router route rm 192.168.0.171/32`
- **THEN** it behaves as `pmox router route delete 192.168.0.171/32`

### Requirement: Top-level shortcuts with full parity

pmox SHALL provide these top-level shortcuts for their canonical commands:

- `launch`, `list`/`ls`, `info`, `start`, `stop`, `restart`, `resize`, `delete`/`rm`
- `shell`, `exec`, `cp`, `sync`, `apply`
- `mount`, and `umount` (for `mount rm`)

A shortcut SHALL accept the same flags and arguments as its canonical command. It SHALL produce the same output and exit codes. The one exception is `info` run with no VM, which shows the Proxmox host (see `list-info-lifecycle`).

#### Scenario: Shortcut and canonical are equivalent
- **WHEN** `pmox list --output json` and `pmox vm list --output json` run against the same cluster
- **THEN** both print identical JSON and exit with the same code

#### Scenario: Flag parity is enforced
- **WHEN** a flag is added to only one of a shortcut and its canonical command
- **THEN** the command-tree test fails

#### Scenario: Resize shortcut
- **WHEN** a user runs `pmox resize web1 --mem 8`
- **THEN** it behaves as `pmox vm resize web1 --mem 8`

### Requirement: Deprecated forms keep working

pmox SHALL keep these old forms working for at least two minor releases after the change that deprecated them:

- `create-template`
- `config get-contexts`, `use-context`, `current-context`, `rename-context`, `delete-context`
- `init --list`, `init --remove <url>`, `init --regen-cloud-init`
- top-level `ssh-config` and `clone`
- `config edit` (now `context edit`)
- `vm resize cpu|ram|disk <vm> <value>` (now `resize <vm> --cpu|--mem|--disk`)
- `apply --init`, `apply --default`, `apply --update` (now `playbook init|default|update`)
- `stop --force` (now `stop --hard`)
- `--server` and `PMOX_SERVER` (now `--context` and `PMOX_CONTEXT`)
- `cleanup --include-templates`, `--include-vms` (now `--include template`, `--include vm`)
- `access revoke --to` (now `--from`)

Each SHALL print one line naming its replacement on stderr, never on stdout. Its stdout output and exit code SHALL be identical to the replacement's. Deprecated commands and flags SHALL NOT appear in help, the palette or shell completion.

#### Scenario: Deprecated command still runs
- **WHEN** a user runs `pmox config use-context lab`
- **THEN** the current context becomes `lab`
- **AND** stderr shows one line pointing at `pmox context use`

#### Scenario: JSON stays clean
- **WHEN** a script runs `pmox config get-contexts --output json`
- **THEN** stdout contains only the same JSON `pmox context list --output json` prints

#### Scenario: Deprecated flag still works
- **WHEN** a user runs `pmox stop --force web1`
- **THEN** `web1` is powered off as with `--hard`
- **AND** stderr shows one line pointing at `--hard`

#### Scenario: Deprecated environment variable
- **WHEN** `PMOX_SERVER=lab pmox list` runs
- **THEN** the `lab` context is listed and stderr shows one line pointing at `PMOX_CONTEXT`

## ADDED Requirements

### Requirement: remount is not in root help
`pmox remount` SHALL keep working as another spelling of `pmox mount restart`, without a deprecation note, and SHALL NOT be listed in root `--help` or the root palette. Root help SHALL list `mount` and `umount`.

#### Scenario: Root help
- **WHEN** a user runs `pmox --help`
- **THEN** `remount` is not listed and `mount` and `umount` are

#### Scenario: Still runs
- **WHEN** a user runs `pmox remount --stale`
- **THEN** stale mounts are restarted and no note is printed

### Requirement: Palette filter searches the whole tree
Typing in the command palette SHALL match commands at every depth below the menu being shown, not only its own rows. A match from a deeper level SHALL be shown with its full path (for example `vm resize`) and choosing it SHALL run that command. With an empty filter the palette SHALL show the current level only, as before. Shortcuts and their canonical commands SHALL appear once.

#### Scenario: Nested command from the root
- **WHEN** a user opens bare `pmox` and types `autostart`
- **THEN** `mount autostart` is listed and Enter runs it

#### Scenario: No duplicates
- **WHEN** a user types `list` at the root
- **THEN** the VM listing appears once, not as both `list` and `vm list`

#### Scenario: Empty filter
- **WHEN** the filter is empty at the root
- **THEN** only root commands and groups are shown

### Requirement: Help text conventions
Every runnable command SHALL have an Examples section with one to four runnable examples. Help prose SHALL describe current behaviour only: it SHALL NOT compare with earlier versions ("as before", "same as always", "still"), name API endpoints or internal limits, or refer to commands by retired names. A subcommand's help SHALL NOT repeat the global flags; it SHALL end with one line pointing at `pmox --help` for them.

#### Scenario: Examples everywhere
- **WHEN** a runnable command without an Examples section is added
- **THEN** a command-tree test fails

#### Scenario: No retired names
- **WHEN** a user runs `pmox init --help`
- **THEN** the text does not contain `configure`

#### Scenario: Global flags are not repeated
- **WHEN** a user runs `pmox shell --help`
- **THEN** the page lists `shell`'s own flags and one line naming `pmox --help` for global flags
