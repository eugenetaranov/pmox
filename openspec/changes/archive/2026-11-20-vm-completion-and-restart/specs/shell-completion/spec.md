# Spec Delta

## Purpose

Dynamic shell completion for the names a user would otherwise copy from `pmox list` or `pmox context list`, and the rules that keep completion fast and silent.

## ADDED Requirements

### Requirement: VM names complete
For every command that takes a `<name|vmid>` argument, shell completion SHALL offer the names of pmox-tagged VMs on the targeted context, each with a description giving its VMID and status. Commands that accept several VMs SHALL keep offering names and SHALL leave out ones already typed. Typing digits SHALL offer matching VMIDs.

#### Scenario: Complete a shell target
- **WHEN** pmox VMs `web1`, `web2` and `db1` exist and a user types `pmox shell w<Tab>`
- **THEN** `web1` and `web2` are offered

#### Scenario: Several targets
- **WHEN** a user types `pmox stop web1 <Tab>`
- **THEN** `web2` and `db1` are offered and `web1` is not

#### Scenario: Another context
- **WHEN** a user types `pmox --context prod shell <Tab>`
- **THEN** the VMs offered are those of `prod`

### Requirement: Only fitting VMs are offered
Completion SHALL narrow the offer by state where the command makes the other state pointless: `start` offers stopped VMs, `stop` offers running VMs. All other commands SHALL offer every pmox VM.

#### Scenario: Start
- **WHEN** `web1` is running and `db1` is stopped and a user types `pmox start <Tab>`
- **THEN** only `db1` is offered

### Requirement: Remote targets complete the VM part
For `cp`, `sync`, `mount`, `umount` and `remount`, completion of an argument that may be a `<vm>:<path>` target SHALL offer VM names followed by `:` with no trailing space, alongside the shell's own file completion for local paths. `umount` and `remount` SHALL offer the targets of existing mounts in full.

#### Scenario: Copy destination
- **WHEN** a user types `pmox cp ./app.tar w<Tab>` and `web1` is the only match
- **THEN** the word becomes `web1:` with the cursor directly after the colon

#### Scenario: Stop a mount
- **WHEN** a mount `web1:/opt/app` is running and a user types `pmox umount <Tab>`
- **THEN** `web1:/opt/app` is offered

### Requirement: Context, template and placement names complete
Completion SHALL offer configured context names for `--context`, `context use`, `context rename`, `context delete` and `config edit`; template names for `launch --template`; and the cluster's nodes, storages and bridges for the `--node`, `--storage`, `--snippet-storage` and `--bridge` flags of `launch` and `clone`.

#### Scenario: Context flag
- **WHEN** contexts `lab` and `prod` are configured and a user types `pmox --context <Tab>`
- **THEN** `lab` and `prod` are offered

#### Scenario: Template flag
- **WHEN** a user types `pmox launch web3 --template <Tab>`
- **THEN** the cluster's template names are offered

### Requirement: Completion is silent and bounded
Completion SHALL never prompt, never draw a picker, never change configuration or pinned keys, and never write anything but candidates to the shell. When the cluster cannot be reached within two seconds, or pmox is not configured, completion SHALL offer nothing and return success. Results from the cluster SHALL be reused for a short time so that repeated Tabs do not each call the API.

#### Scenario: Cluster unreachable
- **WHEN** the API does not answer and a user types `pmox shell <Tab>`
- **THEN** within about two seconds the shell shows no candidates and no error text

#### Scenario: Not configured
- **WHEN** no context is configured and a user types `pmox shell <Tab>`
- **THEN** nothing is offered and nothing is printed

#### Scenario: Repeated Tab
- **WHEN** a user presses Tab twice within a few seconds on the same context
- **THEN** the second completion does not call the cluster

### Requirement: Deprecated forms do not complete
Hidden and deprecated commands and flags SHALL NOT be offered by completion.

#### Scenario: Old command name
- **WHEN** a user types `pmox create-<Tab>`
- **THEN** `create-template` is not offered
