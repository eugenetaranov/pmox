# Spec Delta

## Purpose

Changing a VM's CPU cores, memory and disk size after launch with one command that uses the same flag names and units as `pmox launch`.

## ADDED Requirements

### Requirement: `pmox resize` command
The CLI SHALL expose `pmox vm resize [name|vmid] [--cpu <cores>] [--mem <GiB>] [--disk <+GiB|GiB>]`, with the top-level shortcut `pmox resize`. At least one of the three flags SHALL be required; several MAY be combined in one run. `--mem` SHALL accept fractions such as `1.5`. With no VM named, the command SHALL resolve the target through the shared picker and SHALL NOT use a default VM.

#### Scenario: Memory and cores together
- **WHEN** a user runs `pmox resize web1 --cpu 4 --mem 8`
- **THEN** `web1` is set to 4 cores and 8 GiB, and a running VM is restarted at most once

#### Scenario: Nothing to change
- **WHEN** a user runs `pmox resize web1` with no sizing flag
- **THEN** pmox exits with a user-input error listing `--cpu`, `--mem` and `--disk`

#### Scenario: Value already set
- **WHEN** `web1` has 4 cores and a user runs `pmox resize web1 --cpu 4`
- **THEN** pmox reports that nothing changed and exits 0

### Requirement: Cores and memory apply at the next stop and start
A change to cores or memory on a running VM SHALL take effect only after a stop and start. On a terminal pmox SHALL ask once whether to restart now; `--restart` SHALL restart without asking and `--no-restart` SHALL leave the VM running and say the change is pending. Without a terminal and without either flag, pmox SHALL leave the VM running and say so. A stopped VM SHALL be changed without any question.

#### Scenario: Restart now
- **WHEN** a user runs `pmox resize web1 --mem 16 --restart` on a running VM
- **THEN** the VM is stopped and started and runs with 16 GiB

#### Scenario: Leave for later
- **WHEN** a user runs `pmox resize web1 --mem 16 --no-restart`
- **THEN** the VM keeps running and pmox prints that the change applies at the next stop and start

#### Scenario: Script without either flag
- **WHEN** `pmox resize web1 --cpu 4` runs without a terminal on a running VM
- **THEN** the configuration is changed, the VM is not restarted, and the pending note is printed

### Requirement: Disks only grow
`--disk +N` SHALL grow the disk by N GiB and `--disk N` SHALL grow it to N GiB. A size below the current one SHALL be refused before any change is made. On a running VM pmox SHALL also grow the partition and filesystem without a restart; on a stopped VM they grow at the next boot.

#### Scenario: Grow by
- **WHEN** `web1` has a 20 GiB disk and a user runs `pmox resize web1 --disk +20`
- **THEN** the disk becomes 40 GiB and, if `web1` is running, its filesystem shows the new size

#### Scenario: Shrink refused
- **WHEN** `web1` has a 40 GiB disk and a user runs `pmox resize web1 --disk 20 --mem 8`
- **THEN** pmox exits with a user-input error and neither the disk nor the memory is changed

#### Scenario: Disk with cores, running VM
- **WHEN** a user runs `pmox resize web1 --disk +10 --cpu 4 --restart`
- **THEN** the disk grows, the VM restarts once, and both changes are in effect

### Requirement: Old resize subcommands keep working
`pmox vm resize cpu|ram|disk [name|vmid] <value>` SHALL behave as the matching flag form and print one deprecation line on stderr (see `cli-command-tree`).

#### Scenario: Old form
- **WHEN** a user runs `pmox vm resize ram web1 8`
- **THEN** it behaves as `pmox vm resize web1 --mem 8`
- **AND** stderr shows one line naming the new form
