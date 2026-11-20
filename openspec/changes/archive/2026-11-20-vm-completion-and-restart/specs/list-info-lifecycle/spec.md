# Spec Delta

## ADDED Requirements

### Requirement: `pmox restart` command
The CLI SHALL expose `pmox restart [name|vmid ...]`, which stops each named VM (a graceful shutdown, or a hard power-off with `--hard`), starts it again, and by default waits as `pmox start` does until the guest agent reports an address. A VM that is already stopped SHALL simply be started. `--no-wait` and `--wait` SHALL behave as on `start`. The restart SHALL be a full stop and start, so that pending hardware changes take effect.

#### Scenario: Restart a running VM
- **WHEN** `pmox restart web1` is invoked against a running VM
- **THEN** the VM is shut down, started, and pmox waits for its address
- **AND** reports that `web1` stopped and then that it started, with its vmid and address

#### Scenario: Hard restart
- **WHEN** `pmox restart --hard web1` is invoked
- **THEN** the VM is powered off rather than shut down, then started

#### Scenario: Stopped VM
- **WHEN** `pmox restart web1` is invoked against a stopped VM
- **THEN** it is started and no stop call is made

#### Scenario: Shutdown does not finish
- **WHEN** the shutdown request fails or does not finish in time
- **THEN** pmox exits non-zero, leaves the VM as it is, and suggests `--hard`

#### Scenario: Pending change applies
- **WHEN** a VM's memory was changed while it was running and `pmox restart` completes
- **THEN** the VM runs with the new memory size

### Requirement: restart target selection
With no VM named, `pmox restart` SHALL resolve the target like `pmox stop`: the only pmox VM when there is one, a multi-select picker on a terminal when there are several, and the missing-argument error otherwise. It SHALL NOT use a default VM. Several VMs SHALL be restarted one after another, and a failure SHALL be reported per VM with a non-zero exit at the end.

#### Scenario: Several VMs named
- **WHEN** `pmox restart web1 web2` is invoked and `web2` fails to start
- **THEN** `web1` is reported as restarted, `web2` as failed, and the exit code is non-zero

#### Scenario: Bare in a script
- **WHEN** `pmox restart` runs without a terminal and three pmox VMs exist
- **THEN** it exits with the missing-argument error
