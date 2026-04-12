## Context

pmox already wraps `rsync` in the `sync` command with full SSH target resolution (IP discovery, auto-start, identity key, tag check). The `mount` command builds directly on this — it's a watched loop around the same rsync invocation.

The key architectural insight: in pmox's remote-hypervisor model, a "mount" is really a continuous one-directional sync (local → VM). True filesystem mounts (NFS, SSHFS, 9P) are possible but serve different use cases and have heavier requirements. The watched-rsync approach reuses existing infrastructure and works through NAT, firewalls, and VPNs with zero additional setup.

## Goals

1. `pmox mount ./src vm1:/opt/app` starts continuous sync with a single command
2. Sensible defaults: `.gitignore` filtering, `.git` exclusion, `--delete` for consistency
3. Foreground by default (consistent with all other pmox commands), optional daemon mode
4. Clean shutdown with final sync on Ctrl-C / SIGTERM
5. `pmox umount` to stop daemon-mode mounts

## Non-Goals

- Bidirectional sync (VM → local). One-directional covers the primary edit-locally-run-remotely workflow.
- True POSIX mount semantics (SSHFS, NFS, 9P). These are different features with different requirements.
- Mount persistence in config (auto-remount on VM start). Deferred to a future change.
- Windows support.

## Decisions

### D1. Watched rsync over SSH

Use Go's `fsnotify` library to watch the local directory tree for create/modify/delete/rename events. On any event, debounce for 300ms (configurable via `--debounce`), then shell out to `rsync` using the same SSH transport and target resolution as `pmox sync`.

The rsync invocation uses these default flags:
```
rsync -az --partial --delete --filter=':- .gitignore' --exclude=.git
```

- `-a`: archive mode (preserves permissions, timestamps, symlinks)
- `-z`: compress during transfer (helps over WAN)
- `--partial`: keep partial files on interrupted transfer
- `--delete`: remove files from VM that no longer exist locally
- `--filter=':- .gitignore'`: respect `.gitignore` files at each directory level
- `--exclude=.git`: never sync the `.git` directory

Users can disable gitignore filtering with `--no-gitignore` and disable `--delete` with `--no-delete`. Extra rsync flags can be appended after `--`.

**Why rsync over alternatives**: rsync is already a dependency (`pmox sync`), handles incremental transfer efficiently, respects `.gitignore` natively via filter rules, and works over the SSH transport we already build. No additional infrastructure on the VM or PVE node.

### D7. Exclude patterns: built-in defaults with override

pmox ships with a built-in default exclude list covering common development artifacts:

```
.git
.venv
.terraform
.terraform.*
node_modules
__pycache__
.DS_Store
*.swp
*.swo
*~
```

These defaults are always applied **unless** the user passes `--exclude` / `-x`, which **replaces** the entire default list. This follows the principle of least surprise: defaults work out of the box for most projects, and explicit `--exclude` means "I know what I want to exclude."

```
# uses built-in defaults
pmox mount ./src web1:/opt/app

# replaces defaults entirely — only excludes .git and *.log
pmox mount --exclude=.git --exclude='*.log' ./src web1:/opt/app
```

The built-in list can also be overridden globally via `mount_excludes` in `config.yaml`:

```yaml
mount_excludes:
  - ".git"
  - "node_modules/"
  - ".env"
```

**Resolution order**:
1. If `--exclude` / `-x` is passed on the command line → use those, ignore defaults and config
2. Else if `mount_excludes` exists in config → use those, ignore defaults
3. Else → use built-in defaults

`.gitignore` filtering (`--filter=':- .gitignore'`) is independent and controlled by `--no-gitignore`. It stacks on top of whichever exclude list is active.

**Why override instead of merge**: Merging is harder to reason about. If a user passes `--exclude`, they're being explicit — mixing in hidden defaults creates confusion. The built-in list is printed by `pmox mount --help` so users can copy and adapt it.

### D2. Foreground process with optional daemon mode

Default: foreground. Prints sync events to stderr, Ctrl-C stops the mount with a final sync. Consistent with how `shell`, `exec`, `cp`, and `sync` all run.

`--daemon` / `-d` flag: backgrounds the process, writes a PID file to `$XDG_STATE_HOME/pmox/mounts/<vm>-<hash>.pid` (where hash is derived from local+remote paths), and exits. The `umount` command reads PID files to find and signal the mount process.

**Why PID files over a long-running daemon**: pmox is a CLI tool, not a service. Each mount is an independent process. PID files are simple, portable, and don't require IPC. If the process dies, the stale PID file is detected and cleaned up on next `mount` or `umount`.

### D3. fsnotify overflow fallback

`fsnotify` has a per-watcher event buffer. When watching large directory trees, buffer overflow is possible. On overflow, the watcher emits an `fsnotify.Overflow` event. When this happens, pmox falls back to a full rsync (same as the initial sync) to catch any missed changes.

Additionally, `fsnotify` watches are per-directory — new subdirectories created after the watch starts need to be explicitly added. The watcher loop detects `Create` events on directories and recursively adds watches for them.

### D4. Initial full sync on start

When `pmox mount` starts, it runs a full rsync before entering the watch loop. This establishes a consistent baseline regardless of prior state. The initial sync output is shown to the user so they can see what was transferred.

### D5. Argument syntax: local-only source

Unlike `cp` and `sync` which support both directions (local→VM, VM→local), `mount` only supports local→VM. The source is always a local directory path, and the destination always uses `<name>:<path>` syntax. This keeps the mental model simple: "mount my local directory into the VM."

`umount` takes only the `<name>:<path>` argument to identify which mount to stop.

### D6. Clean shutdown sequence

On SIGINT or SIGTERM:
1. Cancel the fsnotify watcher
2. Run one final rsync to flush any pending changes
3. Remove the PID file (if daemon mode)
4. Exit 0

This ensures the VM has the latest state even if the user Ctrl-C's during a debounce window.

## Risks

- **[Risk] fsnotify misses events on Linux with inotify limits**: Large directory trees may exhaust `fs.inotify.max_user_watches`. → Mitigation: detect the error, print an actionable message suggesting `sysctl fs.inotify.max_user_watches=524288`, and fall back to periodic full rsync.
- **[Risk] rsync not installed on VM**: rsync must be present on the VM for rsync to work. Cloud images usually include it, but minimal images might not. → Mitigation: if the first rsync fails with exit code 127, print a message suggesting `pmox exec <vm> -- sudo apt install rsync`.
- **[Risk] High-frequency saves cause rsync storms**: Rapid file saves (e.g., IDE auto-save) could trigger many rsync invocations. → Mitigation: 300ms debounce window collapses rapid events; rsync is a no-op when nothing changed.
- **[Trade-off] One-directional only**: Files created on the VM are not synced back. This is intentional — bidirectional sync introduces conflict resolution complexity. Users can use `pmox sync vm1:/path ./local` for the reverse direction.
