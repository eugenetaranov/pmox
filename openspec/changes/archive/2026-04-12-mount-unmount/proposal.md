## Why

pmox has `cp` for one-shot file copies and `sync` for manual rsync, but there's no way to keep a local directory continuously synchronized with a VM. Developers working on remote VMs need to edit code locally and see changes reflected immediately — the core "multipass mount" workflow. Today they must re-run `pmox sync` after every edit, which breaks flow.

Unlike Multipass where the hypervisor runs locally, pmox targets a remote Proxmox cluster. True filesystem mounts (9P, virtio-fs) would share the PVE node's filesystem, not the user's laptop. The practical solution is a watched rsync: pmox monitors local filesystem events and incrementally syncs changes to the VM over SSH — the same transport every other pmox command uses.

## What Changes

- Add `pmox mount <local_path> <name>:<remote_path>` command that watches a local directory for changes and continuously rsyncs them to the VM
- Add `pmox umount <name>:<remote_path>` command that stops a running mount by signaling the background process
- The mount process runs in the foreground by default, with a `--daemon` / `-d` flag for background operation
- Default rsync flags include `--delete`, `.gitignore` filtering, and `.git` exclusion for a sensible developer experience
- Exclude patterns supported at two levels: per-command `--exclude` / `-x` flag (repeatable) and global `mount_excludes` list in `config.yaml`
- Mount state is tracked via PID files in `$XDG_STATE_HOME/pmox/mounts/` for daemon mode

## Capabilities

### New Capabilities
- `mount-command`: Continuous watched-rsync from local directory to VM, with foreground and daemon modes, gitignore-aware filtering, and clean lifecycle management via umount

### Modified Capabilities

(none)

## Impact

- **New files**: `cmd/pmox/mount.go` (mount + umount commands), may use Go `fsnotify` library for filesystem watching
- **Modified files**: `cmd/pmox/main.go` (register new commands)
- **Breaking?**: No. Only adds commands.
- **Dependencies**: `github.com/fsnotify/fsnotify` for cross-platform filesystem event watching. System `rsync` binary (already required by `pmox sync`).
- **Cross-slice contract**: None. Fully independent of other capabilities.
