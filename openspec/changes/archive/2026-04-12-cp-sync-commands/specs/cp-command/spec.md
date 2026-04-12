## ADDED Requirements

### Requirement: `pmox cp` command

The CLI SHALL expose `pmox cp <source> <destination>` which copies files between the local host and a pmox-managed VM using the system `scp` binary. Exactly one of source or destination SHALL use the `<name|vmid>:<path>` syntax to identify the remote side. The part before `:` is resolved via the existing VM resolution logic.

The command SHALL accept `--user` / `-u` (default `"pmox"`), `--identity` / `-i`, and `--force` flags with identical behavior to `pmox shell`.

The command SHALL accept `--recursive` / `-r` to enable recursive directory copy, which passes `-r` to scp.

Additional flags MAY be passed after `--` and SHALL be appended verbatim to the scp invocation.

#### Scenario: Copy local file to VM
- **WHEN** `pmox cp ./app.tar.gz web1:/tmp/` is invoked against a running pmox-tagged VM with IP `192.168.1.10`
- **THEN** the command SHALL run `scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key> ./app.tar.gz pmox@192.168.1.10:/tmp/`
- **AND** pmox SHALL exit with scp's exit code

#### Scenario: Copy file from VM to local
- **WHEN** `pmox cp web1:/var/log/syslog ./logs/` is invoked
- **THEN** the command SHALL run `scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key> pmox@192.168.1.10:/var/log/syslog ./logs/`

#### Scenario: Recursive directory copy
- **WHEN** `pmox cp -r ./config/ web1:/etc/app/` is invoked
- **THEN** the command SHALL pass `-r` to scp

#### Scenario: Extra flags via `--`
- **WHEN** `pmox cp ./big.tar web1:/tmp/ -- -l 1000` is invoked
- **THEN** the command SHALL append `-l 1000` to the scp arguments

#### Scenario: Custom user and identity
- **WHEN** `pmox cp --user ubuntu --identity ~/.ssh/custom ./f web1:/tmp/` is invoked
- **THEN** the scp command SHALL use `ubuntu@<ip>` and `-i ~/.ssh/custom`

### Requirement: Argument parsing for remote path

The command SHALL detect which argument is remote by scanning for the first `:` in each positional argument. The text before `:` is the VM name or VMID; the text after `:` is the remote path.

#### Scenario: Both arguments are local
- **WHEN** `pmox cp ./a ./b` is invoked with neither argument containing `:`
- **THEN** the command SHALL exit non-zero with an error stating that exactly one argument must reference a VM

#### Scenario: Both arguments are remote
- **WHEN** `pmox cp vm1:/a vm2:/b` is invoked
- **THEN** the command SHALL exit non-zero with an error stating that VM-to-VM copy is not supported

#### Scenario: Colon in local path
- **WHEN** a local path contains `:` (e.g., `./foo:bar`)
- **THEN** the user SHALL prefix with `./` or use an absolute path; the command resolves `foo` as a VM name otherwise

### Requirement: Auto-start stopped VMs

`pmox cp` SHALL auto-start a stopped VM before copying, using the same sequence as `pmox shell`: power on via PVE API, wait for IP, wait for SSH readiness. Progress messages SHALL be printed to stderr.

#### Scenario: Copy to a stopped VM
- **WHEN** `pmox cp ./file web1:/tmp/` is invoked against a stopped VM
- **THEN** the command SHALL start the VM, wait for SSH readiness, then proceed with scp

### Requirement: Tag check

`pmox cp` SHALL refuse to interact with VMs not tagged `pmox` unless `--force` is passed, consistent with `pmox shell` behavior.

#### Scenario: Untagged VM without force
- **WHEN** `pmox cp ./file legacy:/tmp/` is invoked against an untagged VM
- **THEN** the command SHALL exit non-zero with an error containing `not tagged "pmox"` and mentioning `--force`

#### Scenario: Force bypasses tag check
- **WHEN** `pmox cp --force ./file legacy:/tmp/` is invoked
- **THEN** the command SHALL proceed with the copy

### Requirement: scp binary required

The command SHALL look up `scp` via `exec.LookPath`. If not found, the command SHALL exit non-zero with an error stating that `scp` was not found on PATH.

#### Scenario: scp not installed
- **WHEN** the `scp` binary is not on PATH
- **THEN** the command SHALL exit non-zero with an error stating `scp` was not found
