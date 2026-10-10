## MODIFIED Requirements

### Requirement: `pmox mount` command

The CLI SHALL expose `pmox mount <local_path> [<name|vmid>:]<remote_path>` which watches a local directory for filesystem changes and continuously synchronizes them to a pmox-managed VM using `rsync` over SSH. The source SHALL always be a local directory. The destination SHALL accept both `<name|vmid>:<remote_path>` (explicit form) and a bare `<remote_path>` (picker form).

When the destination is supplied in the explicit `<name|vmid>:<remote_path>` form, the command SHALL resolve the VM via the existing name/vmid lookup path with no picker interaction.

When the destination is a bare `<remote_path>` with no `<name|vmid>:` prefix, the command SHALL delegate VM resolution to the shared target picker defined in the `interactive-target-picker` capability: exactly one pmox VM auto-selects, multiple pmox VMs show an interactive picker when stdin and stderr are TTYs, and non-interactive / zero-VM cases error out with the same messages used by `pmox shell`.

When invoked interactively with missing arguments, the command SHALL use the fields defined in the `remote-target-input` capability:
- With no arguments: a local path field (default `.`, directories only), then a target field.
- With only `<local_path>`: the target field only.

The target field's default path SHALL be `/mnt/<base name of the absolute local path>`. Without a terminal, missing arguments SHALL remain a usage error.

The command SHALL accept `--mkdir` and SHALL confirm and create a missing remote directory as defined in `remote-target-input`, before the initial sync.

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

#### Scenario: Bare `pmox mount` on a terminal
- **WHEN** `pmox mount` is invoked with no arguments in `~/code/src` on a terminal, with web1 the only pmox VM
- **THEN** the local field shows `.` greyed, and after Enter the target field opens as `web1:` with `/mnt/src` greyed
- **AND** Enter on both starts the mount of `.` to `web1:/mnt/src`

#### Scenario: Local path given, target asked
- **WHEN** `pmox mount ./src` is invoked on a terminal
- **THEN** only the target field is shown, with default path `/mnt/src`

### Requirement: `pmox umount` command

The CLI SHALL expose `pmox umount [<name|vmid>:<remote_path>]` which stops running daemon-mode mounts by finding their PID files and sending SIGTERM to each process. The command SHALL accept `--all` to stop all mounts for a given VM when used with a bare `<name|vmid>` argument. The positional argument SHALL be optional.

When invoked with an explicit `<name|vmid>:<remote_path>` argument, the command SHALL behave exactly as it does today: it locates the matching PID file for that VM + remote-path combination and sends SIGTERM.

When invoked with `--all <name|vmid>` (no colon), the command SHALL behave exactly as it does today: it stops every mount whose PID file is prefixed with that VM name.

When invoked with no positional arguments, the command SHALL read the running daemon-mode mounts from the local state directory without contacting the cluster:
- None running: print "✓ No active mounts" and exit 0.
- Exactly one running: stop it without asking.
- Several running, on a terminal: show a multi-select of them as `<local> → <vm>:<path>` and stop the ones chosen.
- Several running, without a terminal: exit non-zero with the existing missing-argument error.

When the requested mount is not running (explicit target, or `--all` with none), the command SHALL report that with a "✓" line and exit 0, as `cli-progress-feedback` requires.

The `pmox umount --help` output SHALL document that calling `pmox umount` with no arguments offers the running mounts to stop.

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

#### Scenario: Bare `pmox umount`, nothing running
- **WHEN** `pmox umount` is invoked with no positional arguments and no mount is running
- **THEN** the command SHALL print "✓ No active mounts" and exit 0
- **AND** SHALL NOT contact the cluster

#### Scenario: Bare `pmox umount`, one mount running
- **WHEN** `pmox umount` is invoked with no positional arguments and exactly one mount is running
- **THEN** the command SHALL stop it without prompting

#### Scenario: Bare `pmox umount`, several mounts, interactive TTY
- **WHEN** `pmox umount` is invoked with no positional arguments, several mounts are running, and stdin and stderr are TTYs
- **THEN** the command SHALL list them as `<local> → <vm>:<path>` in a multi-select
- **AND** SHALL stop each mount chosen

#### Scenario: Bare `pmox umount`, several mounts, non-TTY
- **WHEN** `pmox umount` is invoked with no positional arguments, several mounts are running, and stdin or stderr is not a TTY
- **THEN** the command SHALL exit non-zero without prompting
- **AND** the error SHALL match the existing missing-argument behavior

#### Scenario: No matching mount for explicit target
- **WHEN** `pmox umount web1:/opt/app` is invoked but no daemon mount exists for that path
- **THEN** the command SHALL print a "✓" line stating no mount is running there
- **AND** SHALL exit 0

#### Scenario: Stale PID file
- **WHEN** `pmox umount web1:/opt/app` is invoked and the PID file references a dead process
- **THEN** the command SHALL remove the stale PID file and report that the mount was not running
