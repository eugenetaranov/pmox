## 1. Dependencies and scaffolding

- [x] 1.1 `go get github.com/fsnotify/fsnotify`
- [x] 1.2 Create `cmd/pmox/mount.go` with `newMountCmd()` and `newUmountCmd()` stubs
- [x] 1.3 Register `mount` and `umount` commands in `main.go`

## 2. Mount command — core watch loop

- [x] 2.1 Implement `runMount()`: parse args (local dir + `<name>:<path>`), validate source is a directory, look up rsync, resolve SSH target with auto-start
- [x] 2.2 Build default rsync args: `-az --partial --delete --filter=':- .gitignore' --exclude=.git` with `--no-gitignore` and `--no-delete` flags
- [x] 2.3a Define built-in default exclude list as a package-level var: `.git`, `.venv`, `.terraform`, `.terraform.*`, `node_modules`, `__pycache__`, `.DS_Store`, `*.swp`, `*.swo`, `*~`
- [x] 2.3b Add `mount_excludes` field to `Config` struct in `internal/config/config.go` (top-level `[]string`, YAML key `mount_excludes`)
- [x] 2.3c Implement `--exclude` / `-x` repeatable flag with override semantics: `--exclude` replaces all → else config `mount_excludes` replaces all → else built-in defaults
- [x] 2.3 Implement initial full rsync on start
- [x] 2.4 Implement recursive `fsnotify` watcher: walk the source directory, add watches on all subdirectories, handle `Create` events to add new subdirectory watches
- [x] 2.5 Implement debounce loop: collect events for `--debounce` duration (default 300ms), then trigger incremental rsync
- [x] 2.6 Handle `fsnotify.Overflow` — log warning and trigger full rsync
- [x] 2.7 Print sync activity to stderr: timestamp, number of files changed, rsync transfer summary

## 3. Signal handling and clean shutdown

- [x] 3.1 Hook into existing `signalContext` for SIGINT/SIGTERM
- [x] 3.2 On signal: cancel watcher, run final rsync, exit 0
- [x] 3.3 If rsync is running when signal arrives, wait for it to complete before final sync

## 4. Daemon mode

- [x] 4.1 Implement `--daemon` / `-d` flag: determine state directory (`$XDG_STATE_HOME/pmox/mounts/`), compute PID file path from vm name + path hash
- [x] 4.2 Detect and clean stale PID files (process not running)
- [x] 4.3 Detect duplicate mounts (PID file exists, process alive) and error
- [x] 4.4 Fork background process, write PID file, print PID to stderr, exit parent
- [x] 4.5 In background process: remove PID file on clean shutdown

## 5. Umount command

- [x] 5.1 Implement `runUmount()`: parse `<name>:<path>` argument, find matching PID file
- [x] 5.2 Send SIGTERM, wait for process exit, print confirmation
- [x] 5.3 Implement `--all` flag: glob PID files for the VM name, signal all
- [x] 5.4 Handle stale PID files: remove and report

## 6. Tests

- [x] 6.1 Unit tests for arg parsing (local path + remote `<name>:<path>`, validation errors)
- [x] 6.2 Unit tests for rsync arg building with all flag combinations (`--no-gitignore`, `--no-delete`, `--exclude`, config excludes, extra args via `--`)
- [x] 6.3 Unit tests for PID file path computation (deterministic hash from paths)
- [x] 6.4 Integration-style test for debounce logic (mock fsnotify events, verify rsync call timing)
