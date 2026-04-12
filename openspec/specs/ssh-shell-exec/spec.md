## ADDED Requirements

### Requirement: `pmox shell` command

The CLI SHALL expose `pmox shell <name|vmid>` which resolves the argument to a single pmox-tagged VM, discovers its IPv4 address via the QEMU guest agent, and replaces the current process with an interactive SSH session to that VM using the system `ssh` binary.

The command SHALL accept `--user` / `-u` (default `"pmox"`) to set the SSH login user, and `--identity` / `-i` to set the private key path. When `--identity` is not provided, the command SHALL derive the private key path from the configured `SSHPubkey` by stripping the `.pub` suffix. If neither is available, the command SHALL invoke `ssh` without `-i` and let OpenSSH handle key discovery.

The command SHALL pass `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null` to `ssh` since guest VMs are ephemeral and their host keys change on every launch.

The command SHALL refuse to connect to a VM whose tags do not contain `pmox` unless `--force` is passed. The error message SHALL name the VM, state that it is not tagged `pmox`, and suggest `--force`.

#### Scenario: Interactive shell to a running VM
- **WHEN** `pmox shell web1` is invoked against a running pmox-tagged VM with IP `192.168.1.10`
- **THEN** the command SHALL exec `ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key> pmox@192.168.1.10`
- **AND** the pmox process SHALL be replaced by the SSH process (`syscall.Exec`)

#### Scenario: Custom user and identity
- **WHEN** `pmox shell --user ubuntu --identity ~/.ssh/custom web1` is invoked
- **THEN** the SSH command SHALL use `ubuntu@<ip>` and `-i ~/.ssh/custom`

#### Scenario: No configured key and no --identity flag
- **WHEN** the config has no `SSHPubkey` and `--identity` is not passed
- **THEN** the command SHALL invoke `ssh` without the `-i` flag

#### Scenario: Derived private key does not exist
- **WHEN** the configured `SSHPubkey` is `~/.ssh/id_ed25519.pub` but `~/.ssh/id_ed25519` does not exist
- **THEN** the command SHALL exit non-zero with an error suggesting `--identity`

#### Scenario: Tag check blocks untagged VMs
- **WHEN** `pmox shell legacy` is invoked against a VM not tagged `pmox`
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL contain `not tagged "pmox"` and mention `--force`

#### Scenario: `--force` bypasses the tag check
- **WHEN** `pmox shell --force legacy` is invoked against an untagged VM
- **THEN** the command SHALL proceed with IP discovery and SSH connection

#### Scenario: `ssh` binary not found
- **WHEN** the `ssh` binary is not on PATH
- **THEN** the command SHALL exit non-zero with an error stating that `ssh` was not found

### Requirement: `pmox exec` command

The CLI SHALL expose `pmox exec <name|vmid> -- <command> [args...]` which resolves the argument to a single pmox-tagged VM, discovers its IPv4 address via the QEMU guest agent, and runs a single command over SSH using the system `ssh` binary. The command's stdout, stderr, and exit code SHALL be passed through to the caller.

The command SHALL accept the same `--user`, `--identity`, and `--force` flags as `pmox shell` with identical defaults and behavior.

The `--` separator SHALL be required between the VM argument and the remote command. Arguments after `--` are passed verbatim to `ssh`.

#### Scenario: Run a remote command
- **WHEN** `pmox exec web1 -- uname -a` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL run `ssh ... pmox@<ip> uname -a`
- **AND** stdout and stderr from the remote command SHALL appear on the caller's stdout and stderr
- **AND** pmox SHALL exit with the remote command's exit code

#### Scenario: Remote command fails
- **WHEN** `pmox exec web1 -- false` is invoked
- **THEN** pmox SHALL exit with exit code 1 (the exit code of `false`)

#### Scenario: No command provided
- **WHEN** `pmox exec web1` is invoked without `--` and a command
- **THEN** the command SHALL exit non-zero with a usage error

#### Scenario: Tag check and force flag
- **WHEN** `pmox exec legacy -- whoami` is invoked against an untagged VM
- **THEN** the same tag check behavior as `pmox shell` SHALL apply

### Requirement: Auto-start stopped VMs

Both `pmox shell` and `pmox exec` SHALL auto-start a stopped VM before connecting. The auto-start sequence SHALL be: power on via PVE API, wait for the guest agent to report an IPv4 address, then wait for SSH readiness.

Progress messages SHALL be printed to stderr during the auto-start sequence.

#### Scenario: Shell into a stopped VM
- **WHEN** `pmox shell web1` is invoked against a stopped pmox-tagged VM
- **THEN** the command SHALL start the VM via the PVE API
- **AND** wait for the guest agent to report an IPv4 address
- **AND** wait for SSH readiness
- **AND** then open the SSH session
- **AND** print progress to stderr during the wait

#### Scenario: Exec on a stopped VM
- **WHEN** `pmox exec web1 -- hostname` is invoked against a stopped VM
- **THEN** the same auto-start sequence SHALL apply before running the command

#### Scenario: VM is already gone
- **WHEN** `GetStatus` returns `ErrNotFound`
- **THEN** the command SHALL exit non-zero with an error stating the VM was not found

### Requirement: Guest agent IP discovery

Both commands SHALL discover the VM's IPv4 address by querying the QEMU guest agent via the PVE API. If the guest agent does not respond or returns no usable IPv4 address, the command SHALL fail with an actionable error message.

#### Scenario: Guest agent returns IP
- **WHEN** the guest agent reports interfaces with a usable IPv4 address
- **THEN** the command SHALL use that IP for the SSH connection

#### Scenario: Guest agent not responding on a running VM
- **WHEN** the VM is running but the guest agent does not respond
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL mention `qemu-guest-agent`

#### Scenario: Guest agent returns no IPv4
- **WHEN** the guest agent responds but no interface has a usable IPv4 address
- **THEN** the command SHALL exit non-zero with an error about missing IP
