# Spec Delta

## Purpose

A per-context default VM that the everyday, non-destructive commands target when no VM is named, so that a user with several VMs is not asked to pick the same one each time.

## ADDED Requirements

### Requirement: Setting the default VM
`pmox vm use <name|vmid>` SHALL save that VM as the default for the context in use, after checking that it exists and carries the pmox tag. Without an argument, on a terminal, it SHALL show the VM picker and save the choice; without a terminal it SHALL fail with the missing-argument error. It SHALL print one confirmation line naming the VM and the context.

#### Scenario: Set by name
- **WHEN** a user runs `pmox vm use web1` and `web1` is a pmox VM
- **THEN** `web1` becomes the default VM of the current context
- **AND** pmox prints a line such as `✓ default VM for lab: web1`

#### Scenario: Unknown VM
- **WHEN** a user runs `pmox vm use nosuch`
- **THEN** pmox exits with the not-found error and the default is unchanged

#### Scenario: Pick interactively
- **WHEN** a user runs `pmox vm use` on a terminal with three pmox VMs
- **THEN** the picker is shown and the chosen VM becomes the default

### Requirement: Clearing and showing the default VM
`pmox vm use --clear` SHALL remove the context's default and SHALL succeed when none is set. `pmox list` SHALL mark the default VM's row with `*` and its JSON object with `"default": true`.

#### Scenario: Clear
- **WHEN** a default is set and a user runs `pmox vm use --clear`
- **THEN** no default remains and a bare `pmox shell` with several VMs shows the picker again

#### Scenario: Marker in list
- **WHEN** `web1` is the default and a user runs `pmox list`
- **THEN** only the `web1` row carries `*`

### Requirement: The default is per context
The default VM SHALL be stored with its context in the config file. Switching context, or passing `--context`, SHALL use that context's own default or none.

#### Scenario: Two contexts
- **WHEN** `web1` is the default in `lab`, `prod` has none, and a user runs `pmox --context prod shell`
- **THEN** `web1` is not used and the usual `prod` selection rules apply

### Requirement: Commands that use the default
When no VM is named, `shell`, `exec`, `vm info`, `start`, `apply`, `vm ssh-config`, `yardo push` and `yardo gitkey` SHALL target the default VM without prompting, with or without a terminal and with `--no-input`. Each SHALL say on stderr which VM it chose when the choice came from the default.

#### Scenario: Bare shell
- **WHEN** `web1` is the default, three pmox VMs exist, and a user runs `pmox shell`
- **THEN** pmox opens a shell on `web1` and shows no picker

#### Scenario: In a script
- **WHEN** `web1` is the default and `pmox exec --no-input -- uname -a` runs without a terminal
- **THEN** the command runs on `web1`

### Requirement: Commands that never use the default
`stop`, `delete`, `restart`, `resize`, `clone`'s source and the `access` commands SHALL ignore the default VM and resolve a missing target exactly as they do without one.

#### Scenario: Bare delete
- **WHEN** `web1` is the default, three pmox VMs exist, and a user runs `pmox delete` on a terminal
- **THEN** the multi-select picker is shown with nothing pre-selected

#### Scenario: Bare stop in a script
- **WHEN** `web1` is the default, three pmox VMs exist, and `pmox stop` runs without a terminal
- **THEN** it exits with the missing-argument error

### Requirement: Overriding the default
A named VM SHALL always be used instead of the default. The commands that use the default SHALL accept `--pick`, which ignores the default for that run and applies the usual selection rules.

#### Scenario: Explicit name
- **WHEN** `web1` is the default and a user runs `pmox shell db1`
- **THEN** the shell opens on `db1`

#### Scenario: Pick once
- **WHEN** `web1` is the default and a user runs `pmox shell --pick` on a terminal with three pmox VMs
- **THEN** the picker is shown and the default stays `web1`

### Requirement: A default that no longer exists
When the default VM is not on the cluster, pmox SHALL say so on stderr, clear the stored default, and continue as if none had been set. It SHALL NOT clear the default when the cluster could not be reached. `pmox delete` SHALL clear the default when it deletes that VM.

#### Scenario: Default was deleted elsewhere
- **WHEN** the default `web1` was removed in the Proxmox web UI and a user runs `pmox shell` on a terminal with two other pmox VMs
- **THEN** pmox prints that default VM `web1` no longer exists and was cleared
- **AND** shows the picker

#### Scenario: Delete clears it
- **WHEN** `web1` is the default and `pmox delete web1 --yes` succeeds
- **THEN** the context has no default VM

#### Scenario: Cluster unreachable
- **WHEN** the API cannot be reached while resolving the default
- **THEN** the command fails with the connection error and the stored default is kept

### Requirement: Launch does not set a default
`pmox launch` and `pmox vm clone` SHALL NOT set a default VM and SHALL NOT ask whether to.

#### Scenario: First launch
- **WHEN** no default is set and `pmox launch web1` completes
- **THEN** the context still has no default VM and no question about it was asked
