## MODIFIED Requirements

### Requirement: `pmox mount` command

The CLI SHALL expose `pmox mount <local_path> [<name|vmid>:]<remote_path>` which watches a local directory for filesystem changes and continuously synchronizes them to a pmox-managed VM using `rsync` over SSH. The source SHALL always be a local directory. The destination SHALL accept both `<name|vmid>:<remote_path>` (explicit form) and a bare `<remote_path>` (picker form).

When the destination is supplied in the explicit `<name|vmid>:<remote_path>` form, the command SHALL resolve the VM via the existing name/vmid lookup path with no picker interaction.

When the destination is a bare `<remote_path>` with no `<name|vmid>:` prefix, the command SHALL delegate VM resolution to the shared target picker defined in the `interactive-target-picker` capability: exactly one pmox VM auto-selects, multiple pmox VMs show an interactive picker when stdin and stderr are TTYs, and non-interactive / zero-VM cases error out with the same messages used by `pmox shell`.

The command SHALL accept `--user` / `-u` (default `"pmox"`), `--identity` / `-i`, and `--force` flags with identical behavior to `pmox shell`.

The command SHALL accept `--daemon` / `-d` to run in the background and write a PID file.

The command SHALL accept `--debounce` (default `300ms`) to control the delay between a filesystem event and the rsync invocation.

The command SHALL accept `--no-gitignore` to disable `.gitignore` filtering and `--no-delete` to disable `--delete` from rsync.

The command SHALL ship with a built-in default exclude list: `.git`, `.venv`, `.terraform`, `.terraform.*`, `node_modules`, `__pycache__`, `.DS_Store`, `*.swp`, `*.swo`, `*~`.

The command SHALL accept `--exclude` / `-x` (repeatable) to specify rsync exclude patterns. When `--exclude` is passed, it SHALL **replace** the entire default exclude list.

The command SHALL read `mount_excludes` from the pmox config file (`config.yaml`). When present, it SHALL **replace** the built-in defaults. Per-command `--exclude` takes precedence over config `mount_excludes`.

Additional rsync flags MAY be passed after `--` and SHALL be appended verbatim to every rsync invocation.

The `pmox mount --help` output SHALL document that the `<name|vmid>:` prefix on the destination is optional and SHALL include an example of the bare-path form (e.g. `pmox mount ./src /opt/app`).

#### Scenario: Basic continuous sync with explicit target
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL perform an initial full rsync of `./src` to the VM at `/opt/app`
- **AND** the command SHALL watch `./src` for filesystem events using `fsnotify`
- **AND** on each change (after debounce), the command SHALL run an incremental rsync to the VM
- **AND** the command SHALL print sync activity to stderr
- **AND** the command SHALL NOT invoke the target picker

#### Scenario: Bare destination path, single pmox VM exists
- **WHEN** `pmox mount ./src /opt/app` is invoked with a destination that has no `<name|vmid>:` prefix
- **AND** exactly one pmox-tagged VM exists on the cluster
- **THEN** the command SHALL auto-select that VM without showing a picker
- **AND** the command SHALL proceed with the initial rsync + watch using `/opt/app` as the remote path on the auto-selected VM
- **AND** the command SHALL NOT read from stdin

#### Scenario: Bare destination path, multiple pmox VMs, interactive TTY
- **WHEN** `pmox mount ./src /opt/app` is invoked with a destination that has no `<name|vmid>:` prefix
- **AND** two or more pmox-tagged VMs exist
- **AND** stdin and stderr are both TTYs
- **THEN** the command SHALL display the shared target picker
- **AND** on selection SHALL proceed with the initial rsync + watch against the chosen VM

#### Scenario: Bare destination path, non-TTY stdin
- **WHEN** `pmox mount ./src /opt/app` is invoked with a destination that has no `<name|vmid>:` prefix
- **AND** stdin or stderr is not a TTY
- **THEN** the command SHALL exit non-zero without prompting
- **AND** the error SHALL match the existing missing-argument behavior

#### Scenario: Bare destination path, zero pmox VMs
- **WHEN** `pmox mount ./src /opt/app` is invoked with a destination that has no `<name|vmid>:` prefix
- **AND** no pmox-tagged VMs exist
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL state that no pmox VMs were found and suggest `pmox launch`

#### Scenario: Default rsync flags with built-in excludes
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked with no extra flags
- **THEN** each rsync invocation SHALL include `-az --partial --delete --filter=':- .gitignore'`
- **AND** each rsync invocation SHALL include `--exclude` for each built-in default: `.git`, `.venv`, `.terraform`, `.terraform.*`, `node_modules`, `__pycache__`, `.DS_Store`, `*.swp`, `*.swo`, `*~`

#### Scenario: Disable gitignore filtering
- **WHEN** `pmox mount --no-gitignore ./src web1:/opt/app` is invoked
- **THEN** the rsync invocation SHALL NOT include `--filter=':- .gitignore'`

#### Scenario: Disable delete
- **WHEN** `pmox mount --no-delete ./src web1:/opt/app` is invoked
- **THEN** the rsync invocation SHALL NOT include `--delete`

#### Scenario: Per-command exclude replaces defaults
- **WHEN** `pmox mount --exclude=.git --exclude='*.log' ./src web1:/opt/app` is invoked
- **THEN** the rsync invocation SHALL include `--exclude=.git --exclude=*.log`
- **AND** the rsync invocation SHALL NOT include any of the other built-in defaults (`.venv`, `node_modules`, etc.)

#### Scenario: Config excludes replace defaults
- **WHEN** the config file contains `mount_excludes: [".git", "vendor/"]`
- **AND** `pmox mount ./src web1:/opt/app` is invoked with no `--exclude` flags
- **THEN** the rsync invocation SHALL include `--exclude=.git --exclude=vendor/`
- **AND** the rsync invocation SHALL NOT include any of the other built-in defaults

#### Scenario: Per-command exclude takes precedence over config
- **WHEN** the config contains `mount_excludes: [".git", "vendor/"]`
- **AND** `pmox mount --exclude=.git --exclude='*.log' ./src web1:/opt/app` is invoked
- **THEN** the rsync invocation SHALL include `--exclude=.git --exclude=*.log`
- **AND** the config `mount_excludes` SHALL be ignored

#### Scenario: Extra rsync flags via `--`
- **WHEN** `pmox mount ./src web1:/opt/app -- --exclude=*.log --bwlimit=1000` is invoked
- **THEN** the rsync invocation SHALL append `--exclude=*.log --bwlimit=1000`

#### Scenario: Custom user and identity
- **WHEN** `pmox mount --user ubuntu --identity ~/.ssh/custom ./src web1:/opt/app` is invoked
- **THEN** the rsync `-e` flag SHALL reference `-i ~/.ssh/custom` and the remote path SHALL use `ubuntu@<ip>`

### Requirement: `pmox umount` command

The CLI SHALL expose `pmox umount [<name|vmid>:<remote_path>]` which stops running daemon-mode mounts by finding their PID files and sending SIGTERM to each process. The command SHALL accept `--all` to stop all mounts for a given VM when used with a bare `<name|vmid>` argument. The positional argument SHALL be optional.

When invoked with an explicit `<name|vmid>:<remote_path>` argument, the command SHALL behave exactly as it does today: it locates the matching PID file for that VM + remote-path combination and sends SIGTERM.

When invoked with `--all <name|vmid>` (no colon), the command SHALL behave exactly as it does today: it stops every mount whose PID file is prefixed with that VM name.

When invoked with no positional arguments at all, the command SHALL delegate VM resolution to the shared target picker defined in the `interactive-target-picker` capability (exactly one pmox VM auto-selects silently; multiple VMs show an interactive picker when stdin and stderr are TTYs; non-interactive / zero-VM cases error out). After resolving the VM, the command SHALL stop every daemon-mode mount associated with that VM — equivalent to running `pmox umount --all <resolved-vm>`.

The `pmox umount --help` output SHALL document that calling `pmox umount` with no arguments stops all mounts for the resolved VM.

#### Scenario: Stop a specific mount by explicit target
- **WHEN** `pmox umount web1:/opt/app` is invoked and a daemon mount exists for that path
- **THEN** the command SHALL send SIGTERM to the mount process
- **AND** wait for the process to exit
- **AND** print confirmation to stderr
- **AND** the command SHALL NOT invoke the target picker

#### Scenario: Stop all mounts for a VM via `--all`
- **WHEN** `pmox umount --all web1` is invoked
- **THEN** the command SHALL find all PID files for `web1` and send SIGTERM to each
- **AND** the command SHALL NOT invoke the target picker

#### Scenario: Bare `pmox umount`, single pmox VM exists
- **WHEN** `pmox umount` is invoked with no positional arguments
- **AND** exactly one pmox-tagged VM exists on the cluster
- **THEN** the command SHALL auto-select that VM without showing a picker
- **AND** the command SHALL stop every daemon-mode mount for that VM (equivalent to `pmox umount --all <vm>`)
- **AND** the command SHALL print the number of stopped mounts to stderr

#### Scenario: Bare `pmox umount`, multiple pmox VMs, interactive TTY
- **WHEN** `pmox umount` is invoked with no positional arguments
- **AND** two or more pmox-tagged VMs exist
- **AND** stdin and stderr are both TTYs
- **THEN** the command SHALL display the shared target picker
- **AND** on selection SHALL stop every daemon-mode mount for the chosen VM

#### Scenario: Bare `pmox umount`, non-TTY stdin
- **WHEN** `pmox umount` is invoked with no positional arguments
- **AND** stdin or stderr is not a TTY
- **AND** two or more pmox-tagged VMs exist
- **THEN** the command SHALL exit non-zero without prompting
- **AND** the error SHALL match the existing missing-argument behavior

#### Scenario: Bare `pmox umount`, zero pmox VMs
- **WHEN** `pmox umount` is invoked with no positional arguments
- **AND** no pmox-tagged VMs exist
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL state that no pmox VMs were found and suggest `pmox launch`

#### Scenario: No matching mount for explicit target
- **WHEN** `pmox umount web1:/opt/app` is invoked but no daemon mount exists for that path
- **THEN** the command SHALL exit non-zero with an error stating no mount was found

#### Scenario: Stale PID file
- **WHEN** `pmox umount web1:/opt/app` is invoked and the PID file references a dead process
- **THEN** the command SHALL remove the stale PID file and report that the mount was not running
