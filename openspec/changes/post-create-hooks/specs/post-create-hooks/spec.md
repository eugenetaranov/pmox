## ADDED Requirements

### Requirement: `--post-create` flag

The `launch` and `clone` commands SHALL accept `--post-create <path>` which runs a user-supplied executable after the SSH-wait phase succeeds.

#### Scenario: Script runs after wait-SSH
- **WHEN** `pmox launch --post-create ./provision.sh web1` completes wait-SSH
- **THEN** the launcher SHALL execute `./provision.sh` via `exec.CommandContext`
- **AND** SHALL stream the script's stdout and stderr to the user
- **AND** the execution SHALL happen strictly after the SSH-wait phase

#### Scenario: Script receives VM info via env vars
- **WHEN** a post-create script is executed
- **THEN** the process environment SHALL contain `PMOX_IP`, `PMOX_VMID`, `PMOX_NAME`, `PMOX_USER`, `PMOX_NODE`
- **AND** the existing `os.Environ()` values SHALL be inherited
- **AND** `PMOX_IP` SHALL equal the discovered IPv4
- **AND** `PMOX_VMID` SHALL equal the decimal VMID as a string

#### Scenario: Direct exec, not shell
- **WHEN** a post-create script is invoked
- **THEN** the launcher SHALL NOT wrap the command in `sh -c` or any other shell
- **AND** SHALL invoke the path argument directly so the script's own shebang handles interpretation

### Requirement: `--tack` flag

The `launch` and `clone` commands SHALL accept `--tack <config-path>` which invokes `tack apply --host <ip> --user <user> <config>` as the post-create step.

#### Scenario: Tack argv shape
- **WHEN** `pmox launch --tack ./tack.yaml --user ubuntu web1` reaches the hook phase
- **THEN** the launcher SHALL execute `tack` with argv `[apply --host <ip> --user ubuntu ./tack.yaml]`

#### Scenario: Missing tack binary
- **WHEN** `tack` is not present on `$PATH` and the tack hook is about to run
- **THEN** the hook SHALL return an error containing `tack binary not found`
- **AND** the error message SHALL suggest `--post-create` as an alternative

### Requirement: `--ansible` flag

The `launch` and `clone` commands SHALL accept `--ansible <playbook-path>` which invokes `ansible-playbook` against the new VM as the post-create step.

#### Scenario: Ansible argv shape
- **WHEN** `pmox launch --ansible ./play.yaml --user ubuntu --ssh-key ~/.ssh/id_ed25519 web1` reaches the hook phase
- **THEN** the launcher SHALL execute `ansible-playbook` with argv containing `-i <ip>,`, `-u ubuntu`, `--private-key ~/.ssh/id_ed25519`, and the playbook path as the last argument
- **AND** the `-e` extra-vars SHALL include `pmox_vmid=<vmid>` and `pmox_name=<name>`

#### Scenario: Missing ansible-playbook binary
- **WHEN** `ansible-playbook` is not present on `$PATH`
- **THEN** the hook SHALL return an error containing `ansible-playbook binary not found`

### Requirement: Hook flags are mutually exclusive

The launcher SHALL reject any combination of two or more of `--post-create`, `--tack`, `--ansible` before issuing any PVE API call.

#### Scenario: Combining --tack and --ansible is an error
- **WHEN** `pmox launch --tack ./t.yaml --ansible ./a.yaml web1` is invoked
- **THEN** the command SHALL return an error naming both flags
- **AND** the error message SHALL contain `mutually exclusive`
- **AND** SHALL exit `ExitConfig`
- **AND** SHALL NOT call any PVE API

#### Scenario: Single hook flag is accepted
- **WHEN** exactly one of the three hook flags is passed
- **THEN** the launcher SHALL proceed normally

### Requirement: `--strict-hooks` flag

The `--strict-hooks` flag SHALL upgrade hook failure from a stderr warning (exit 0) to a fatal error mapped to a new `ExitHook` exit code.

#### Scenario: Lenient mode prints warning and exits 0
- **WHEN** a hook exits non-zero and `--strict-hooks` is NOT set
- **THEN** stderr SHALL contain `warning: <hook-name> hook failed: <error>`
- **AND** stdout SHALL print the normal `launched <name> (vmid=..., ip=...)` message
- **AND** the process SHALL exit 0

#### Scenario: Strict mode maps failure to ExitHook
- **WHEN** a hook exits non-zero and `--strict-hooks` IS set
- **THEN** stderr SHALL contain the same warning line
- **AND** the process SHALL exit with exit code `ExitHook`

#### Scenario: Strict mode does not auto-delete the VM
- **WHEN** a strict hook fails
- **THEN** the launcher SHALL NOT issue any `Delete` call
- **AND** the VM SHALL remain on the cluster

### Requirement: `ExitHook` exit code

The `internal/exitcode` package SHALL expose `ExitHook` with a stable numeric value and SHALL map `*launch.HookError` to it via `errors.As`.

#### Scenario: HookError maps to ExitHook
- **WHEN** `exitcode.From(err)` is called with a wrapped `*launch.HookError`
- **THEN** the function SHALL return `ExitHook`

### Requirement: Hooks skipped on `--no-wait-ssh`

The launcher SHALL skip the hook phase when `--no-wait-ssh` is set, and SHALL emit a warning to stderr when both `--no-wait-ssh` and a hook flag are combined.

#### Scenario: no-wait-ssh plus a hook emits warning and skips hook
- **WHEN** `pmox launch --no-wait-ssh --post-create ./p.sh web1` is invoked
- **THEN** stderr SHALL contain `warning: --no-wait-ssh set; hook will not run`
- **AND** the post-create script SHALL NOT be executed
- **AND** the launch SHALL still succeed if the earlier phases succeed

### Requirement: Hook timeout budget

The launcher SHALL grant the hook a timeout derived from the remaining `--wait` budget, with a minimum floor of 30 seconds.

#### Scenario: Hook gets remaining budget
- **WHEN** wait-IP and wait-SSH consumed 40s of a 180s `--wait` budget
- **THEN** the hook SHALL be invoked with a context whose deadline is approximately 140s from the call site

#### Scenario: Hook gets at least 30s floor
- **WHEN** wait-IP and wait-SSH consumed 179s of a 180s budget
- **THEN** the hook SHALL be invoked with a context whose deadline is at least 30s from the call site
