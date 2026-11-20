# Spec Delta

## MODIFIED Requirements

### Requirement: `pmox stop` command

The CLI SHALL expose `pmox stop <name|vmid>` which gracefully
shuts down a running VM. `--hard` SHALL issue a hard power-off
instead. `--force` SHALL remain accepted as a deprecated spelling of
`--hard` (see `cli-command-tree`).

#### Scenario: Default stop is ACPI shutdown
- **WHEN** `pmox stop web1` is invoked
- **THEN** the command SHALL call `Shutdown` (POST `/status/shutdown`)
- **AND** SHALL wait for the resulting task to finish

#### Scenario: `--hard` is a hard stop
- **WHEN** `pmox stop --hard web1` is invoked
- **THEN** the command SHALL call `Stop` (POST `/status/stop`)

#### Scenario: `--force` is a hard stop
- **WHEN** `pmox stop --force web1` is invoked
- **THEN** the command SHALL call `Stop` (POST `/status/stop`)
- **AND** SHALL print one deprecation line naming `--hard` on stderr

#### Scenario: `--no-wait` short-circuits the task wait
- **WHEN** `pmox stop --no-wait web1` is invoked
- **THEN** the command SHALL return as soon as the Shutdown call returns a UPID

## ADDED Requirements

### Requirement: `--hard` and `--force` mean one thing each
On every VM command, `--hard` SHALL mean "power off instead of shutting down" and `--force` SHALL mean "act on a VM without the pmox tag". No command SHALL use either flag for anything else.

#### Scenario: Flags on delete
- **WHEN** a user runs `pmox delete --hard web1`
- **THEN** the VM is powered off before it is destroyed, and the tag check still applies

#### Scenario: Audit
- **WHEN** a command defines `--hard` or `--force` with another meaning
- **THEN** a command-tree test fails

### Requirement: Root `info` shows the host or a VM
`pmox info` with no argument SHALL show the Proxmox host of the context in use and SHALL NOT open a VM picker or use a default VM. `pmox info <name|vmid>` SHALL show that VM exactly as `pmox vm info <name|vmid>` does, including with `--output json`.

#### Scenario: Bare info
- **WHEN** a user runs `pmox info` with several pmox VMs
- **THEN** the host summary is printed and no picker is shown

#### Scenario: Info with a VM
- **WHEN** a user runs `pmox info web1`
- **THEN** the output equals that of `pmox vm info web1`

#### Scenario: Unknown VM
- **WHEN** a user runs `pmox info nosuch`
- **THEN** pmox exits with the VM not-found error, not "unknown command"
