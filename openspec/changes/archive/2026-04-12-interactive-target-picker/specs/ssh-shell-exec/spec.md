## MODIFIED Requirements

### Requirement: `pmox shell` command

The CLI SHALL expose `pmox shell [<name|vmid>]` which resolves the argument to a single pmox-tagged VM, discovers its IPv4 address via the QEMU guest agent, and replaces the current process with an interactive SSH session to that VM using the system `ssh` binary.

The `<name|vmid>` positional argument SHALL be optional. When omitted, the command SHALL delegate target resolution to the shared target picker defined in the `interactive-target-picker` capability: exactly one pmox VM auto-selects, multiple pmox VMs show an interactive picker when stdin and stderr are TTYs, and non-interactive / zero-VM cases error out.

The command SHALL accept `--user` / `-u` (default comes from the server config `user` field, falling back to `"pmox"`) to set the SSH login user, and `--identity` / `-i` to set the private key path. When `--identity` is not provided, the command SHALL derive the private key path from the configured `SSHPubkey` by stripping the `.pub` suffix. If neither is available, the command SHALL invoke `ssh` without `-i` and let OpenSSH handle key discovery.

The command SHALL pass `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null` to `ssh` since guest VMs are ephemeral and their host keys change on every launch.

The command SHALL refuse to connect to a VM whose tags do not contain `pmox` unless `--force` is passed. The error message SHALL name the VM, state that it is not tagged `pmox`, and suggest `--force`.

#### Scenario: Interactive shell to a running VM
- **WHEN** `pmox shell web1` is invoked against a running pmox-tagged VM with IP `192.168.1.10`
- **THEN** the command SHALL exec `ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key> <user>@192.168.1.10`
- **AND** the pmox process SHALL be replaced by the SSH process (`syscall.Exec`)

#### Scenario: Shell without a target, single pmox VM exists
- **WHEN** `pmox shell` is invoked with no positional argument
- **AND** exactly one pmox-tagged VM exists on the cluster
- **THEN** the command SHALL auto-select that VM without showing a picker
- **AND** SHALL proceed with the existing IP-discovery + SSH-exec sequence

#### Scenario: Shell without a target, multiple pmox VMs, interactive TTY
- **WHEN** `pmox shell` is invoked with no positional argument
- **AND** two or more pmox-tagged VMs exist
- **AND** stdin and stderr are both TTYs
- **THEN** the command SHALL display the target picker
- **AND** SHALL proceed with the user's selection

#### Scenario: Shell without a target, non-TTY stdin
- **WHEN** `pmox shell` is invoked with no positional argument and stdin is a pipe
- **THEN** the command SHALL exit non-zero with the existing missing-argument behavior
- **AND** SHALL NOT prompt or draw any UI

#### Scenario: Shell without a target, zero pmox VMs
- **WHEN** `pmox shell` is invoked with no positional argument
- **AND** no pmox-tagged VMs exist
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL state that no pmox VMs were found and suggest `pmox launch`

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

The CLI SHALL expose `pmox exec [<name|vmid>] -- <command> [args...]` which resolves the argument to a single pmox-tagged VM, discovers its IPv4 address via the QEMU guest agent, and runs a single command over SSH using the system `ssh` binary. The command's stdout, stderr, and exit code SHALL be passed through to the caller.

The `<name|vmid>` positional argument SHALL be optional. When omitted, the command SHALL delegate target resolution to the shared target picker defined in the `interactive-target-picker` capability. The `--` separator and a non-empty remote command SHALL remain required; omitting the remote command SHALL still produce the existing usage error regardless of picker state.

The command SHALL accept the same `--user`, `--identity`, and `--force` flags as `pmox shell` with identical defaults and behavior.

Arguments after `--` are passed verbatim to `ssh`.

#### Scenario: Run a remote command
- **WHEN** `pmox exec web1 -- uname -a` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL run `ssh ... <user>@<ip> uname -a`
- **AND** stdout and stderr from the remote command SHALL appear on the caller's stdout and stderr
- **AND** pmox SHALL exit with the remote command's exit code

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
- **WHEN** `pmox exec` is invoked with no positional arguments and no `--`
- **THEN** the command SHALL exit non-zero with the existing usage error
- **AND** SHALL NOT display the picker

#### Scenario: Remote command fails
- **WHEN** `pmox exec web1 -- false` is invoked
- **THEN** pmox SHALL exit with exit code 1 (the exit code of `false`)

#### Scenario: No command provided
- **WHEN** `pmox exec web1` is invoked without `--` and a command
- **THEN** the command SHALL exit non-zero with a usage error

#### Scenario: Tag check and force flag
- **WHEN** `pmox exec legacy -- whoami` is invoked against an untagged VM
- **THEN** the same tag check behavior as `pmox shell` SHALL apply
