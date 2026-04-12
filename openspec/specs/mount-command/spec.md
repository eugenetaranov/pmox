## ADDED Requirements

### Requirement: `pmox mount` command

The CLI SHALL expose `pmox mount <local_path> <name|vmid>:<remote_path>` which watches a local directory for filesystem changes and continuously synchronizes them to a pmox-managed VM using `rsync` over SSH. The source SHALL always be a local directory and the destination SHALL always use `<name|vmid>:<path>` syntax.

The command SHALL accept `--user` / `-u` (default `"pmox"`), `--identity` / `-i`, and `--force` flags with identical behavior to `pmox shell`.

The command SHALL run in the background by default, writing a PID file, and SHALL accept `--foreground` / `-F` to instead run attached to the invoking terminal.

The command SHALL accept `--debounce` (default `300ms`) to control the delay between a filesystem event and the rsync invocation.

The command SHALL accept `--no-gitignore` to disable `.gitignore` filtering and `--no-delete` to disable `--delete` from rsync.

The command SHALL ship with a built-in default exclude list: `.git`, `.venv`, `.terraform`, `.terraform.*`, `node_modules`, `__pycache__`, `.DS_Store`, `*.swp`, `*.swo`, `*~`.

The command SHALL accept `--exclude` / `-x` (repeatable) to specify rsync exclude patterns. When `--exclude` is passed, it SHALL **replace** the entire default exclude list.

The command SHALL read `mount_excludes` from the pmox config file (`config.yaml`). When present, it SHALL **replace** the built-in defaults. Per-command `--exclude` takes precedence over config `mount_excludes`.

Additional rsync flags MAY be passed after `--` and SHALL be appended verbatim to every rsync invocation.

#### Scenario: Basic continuous sync
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked against a running pmox-tagged VM
- **THEN** the command SHALL perform an initial full rsync of `./src` to the VM at `/opt/app`
- **AND** the command SHALL watch `./src` for filesystem events using `fsnotify`
- **AND** on each change (after debounce), the command SHALL run an incremental rsync to the VM
- **AND** the command SHALL print sync activity to stderr

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

### Requirement: Daemon mode (default)

The command SHALL run in daemon mode by default. In daemon mode, the command SHALL fork, write a PID file to `$XDG_STATE_HOME/pmox/mounts/<vm>-<hash>.pid`, and exit immediately. The hash SHALL be derived from the local and remote path combination to allow multiple mounts to coexist. Passing `--foreground` / `-F` SHALL skip daemonization and run the watcher attached to the invoking terminal.

#### Scenario: Default invocation starts a daemon
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked
- **THEN** the command SHALL start the mount process in the background
- **AND** write a PID file containing the process ID
- **AND** exit 0 immediately
- **AND** print the PID and mount paths to stderr

#### Scenario: Foreground flag skips daemonization
- **WHEN** `pmox mount --foreground ./src web1:/opt/app` is invoked
- **THEN** the command SHALL NOT fork
- **AND** SHALL run the initial rsync and fsnotify watcher in the invoking process
- **AND** SHALL NOT write a PID file

#### Scenario: Daemon mode detects stale PID
- **WHEN** a PID file exists but the referenced process is not running
- **THEN** the command SHALL remove the stale PID file and proceed normally

#### Scenario: Duplicate daemon mount
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked and a mount for the same paths is already running
- **THEN** the command SHALL exit non-zero with an error stating the mount is already active

### Requirement: Clean shutdown

On SIGINT or SIGTERM, the command SHALL cancel the filesystem watcher, run one final rsync to flush pending changes, remove the PID file (if running as a daemon), and exit 0.

#### Scenario: Ctrl-C during watch
- **WHEN** the user presses Ctrl-C while `pmox mount` is watching
- **THEN** the command SHALL perform a final rsync
- **AND** exit 0

#### Scenario: SIGTERM in daemon mode
- **WHEN** the daemon process receives SIGTERM
- **THEN** the command SHALL perform a final rsync
- **AND** remove the PID file
- **AND** exit 0

### Requirement: fsnotify overflow handling

When `fsnotify` emits an overflow event (buffer full), the command SHALL fall back to a full rsync to catch any missed changes. New subdirectories created after the watch starts SHALL be recursively added to the watcher.

#### Scenario: Event buffer overflow
- **WHEN** the filesystem event buffer overflows
- **THEN** the command SHALL log a warning to stderr
- **AND** trigger a full rsync

#### Scenario: New subdirectory created
- **WHEN** a new directory is created inside the watched path
- **THEN** the command SHALL add a recursive watch on the new directory

### Requirement: Auto-start stopped VMs

`pmox mount` SHALL auto-start a stopped VM before syncing, using the same sequence as `pmox shell`: power on via PVE API, wait for IP, wait for SSH readiness. Progress messages SHALL be printed to stderr.

#### Scenario: Mount to a stopped VM
- **WHEN** `pmox mount ./src web1:/opt/app` is invoked against a stopped VM
- **THEN** the command SHALL start the VM, wait for SSH readiness, then perform initial sync and start watching

### Requirement: Tag check

`pmox mount` SHALL refuse to interact with VMs not tagged `pmox` unless `--force` is passed, consistent with `pmox shell` behavior.

#### Scenario: Untagged VM without force
- **WHEN** `pmox mount ./src legacy:/opt/app` is invoked against an untagged VM
- **THEN** the command SHALL exit non-zero with an error containing `not tagged "pmox"` and mentioning `--force`

### Requirement: rsync binary required

The command SHALL look up `rsync` via `exec.LookPath`. If not found, the command SHALL exit non-zero with an error stating that `rsync` was not found on PATH.

#### Scenario: rsync not installed
- **WHEN** the `rsync` binary is not on PATH
- **THEN** the command SHALL exit non-zero with an error stating `rsync` was not found

### Requirement: Source must be a directory

The local source path SHALL be validated as an existing directory. If it does not exist or is not a directory, the command SHALL exit non-zero with an actionable error.

#### Scenario: Source path does not exist
- **WHEN** `pmox mount ./nonexistent web1:/opt/app` is invoked and `./nonexistent` does not exist
- **THEN** the command SHALL exit non-zero with an error stating the source directory was not found

#### Scenario: Source path is a file
- **WHEN** `pmox mount ./main.go web1:/opt/app` is invoked and `./main.go` is a regular file
- **THEN** the command SHALL exit non-zero with an error stating the source must be a directory

### Requirement: `pmox umount` command

The CLI SHALL expose `pmox umount <name|vmid>:<remote_path>` which stops a running daemon-mode mount by finding its PID file and sending SIGTERM to the process. The command SHALL also accept `--all` to stop all mounts for a given VM.

#### Scenario: Stop a specific mount
- **WHEN** `pmox umount web1:/opt/app` is invoked and a daemon mount exists for that path
- **THEN** the command SHALL send SIGTERM to the mount process
- **AND** wait for the process to exit
- **AND** print confirmation to stderr

#### Scenario: Stop all mounts for a VM
- **WHEN** `pmox umount --all web1` is invoked
- **THEN** the command SHALL find all PID files for `web1` and send SIGTERM to each

#### Scenario: No matching mount
- **WHEN** `pmox umount web1:/opt/app` is invoked but no daemon mount exists for that path
- **THEN** the command SHALL exit non-zero with an error stating no mount was found

#### Scenario: Stale PID file
- **WHEN** `pmox umount web1:/opt/app` is invoked and the PID file references a dead process
- **THEN** the command SHALL remove the stale PID file and report that the mount was not running
