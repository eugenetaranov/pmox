# Spec Delta

## MODIFIED Requirements

### Requirement: `pmox exec` command

The CLI SHALL expose `pmox exec [<name|vmid>] -- <command> [args...]` which resolves the argument to a single pmox-tagged VM, discovers its IPv4 address via the QEMU guest agent, and runs a single command over SSH using the system `ssh` binary. The command's stdout, stderr, and exit code SHALL be passed through to the caller.

The `<name|vmid>` positional argument SHALL be optional. When omitted, the command SHALL delegate target resolution to the shared target picker defined in the `interactive-target-picker` capability.

The `--` separator SHALL be optional when a VM is named: in `pmox exec <name|vmid> <command> [args...]` the first positional argument SHALL be the VM and every later argument, including ones that begin with `-`, SHALL be the remote command, passed through unchanged. In this form pmox's own flags SHALL be given before the VM. When `--` is present the command SHALL behave as before: pmox flags may appear anywhere before `--`, and the VM may be omitted.

A non-empty remote command SHALL remain required without a terminal; omitting it SHALL produce the existing usage error regardless of picker state.

The command SHALL accept the same `--user`, `--identity`, and `--force` flags as `pmox shell` with identical defaults and behavior.

Arguments after `--` are passed verbatim to `ssh`.

#### Scenario: Run a remote command
- **WHEN** `pmox exec web1 -- uname -a` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL run `ssh ... <user>@<ip> uname -a`
- **AND** stdout and stderr from the remote command SHALL appear on the caller's stdout and stderr
- **AND** pmox SHALL exit with the remote command's exit code

#### Scenario: Run a remote command without the separator
- **WHEN** `pmox exec web1 uname -a` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL run `uname -a` on `web1`
- **AND** `-a` SHALL NOT be parsed as a pmox flag

#### Scenario: pmox flags before the VM without the separator
- **WHEN** `pmox exec -u root web1 ls -l /root` is invoked
- **THEN** pmox SHALL log in as `root` and run `ls -l /root`

#### Scenario: First argument is not a VM
- **WHEN** `pmox exec uname -a` is invoked and no VM is named `uname`
- **THEN** the command SHALL exit non-zero with the not-found error
- **AND** the error SHALL add that `pmox exec -- uname -a` runs the command on the default or only VM

#### Scenario: Exec without a target, single pmox VM exists
- **WHEN** `pmox exec -- uname -a` is invoked with no VM positional argument
- **AND** exactly one pmox-tagged VM exists
- **THEN** the command SHALL auto-select that VM
- **AND** SHALL run the remote command against it

#### Scenario: Exec without a target, multiple pmox VMs, interactive TTY
- **WHEN** `pmox exec -- uname -a` is invoked with no VM positional argument
- **AND** two or more pmox-tagged VMs exist
- **AND** stdin and stderr are both TTYs
- **THEN** the command SHALL display the target picker before running the remote command

#### Scenario: Exec without a target and without a remote command
- **WHEN** `pmox exec` is invoked with no positional arguments and no `--`, without a terminal
- **THEN** the command SHALL exit non-zero with the existing usage error
- **AND** SHALL NOT display the picker

#### Scenario: Remote command fails
- **WHEN** `pmox exec web1 -- false` is invoked
- **THEN** pmox SHALL exit with exit code 1 (the exit code of `false`)

#### Scenario: No command provided
- **WHEN** `pmox exec web1` is invoked without a command and without a terminal
- **THEN** the command SHALL exit non-zero with a usage error

#### Scenario: Tag check and force flag
- **WHEN** `pmox exec legacy -- whoami` is invoked against an untagged VM
- **THEN** the same tag check behavior as `pmox shell` SHALL apply
