## Why

pmox can shell into VMs and run remote commands, but there's no way to copy files to or from a VM. Users currently have to manually construct scp/rsync commands with the right IP, key, and user — defeating the purpose of a multipass-style CLI that abstracts away VM connection details.

## What Changes

- Add `pmox cp` command that wraps `scp` to copy files between local and remote VMs, supporting both directions (local→VM, VM→local) using the `<name>:<path>` syntax
- Add `pmox sync` command that wraps `rsync` over SSH for efficient directory synchronization in both directions
- Both commands reuse the existing SSH target resolution (IP discovery, auto-start, identity key derivation, tag check) from `pmox shell` / `pmox exec`

## Capabilities

### New Capabilities
- `cp-command`: The `pmox cp` command wrapping scp for bidirectional file copy between local host and VMs
- `sync-command`: The `pmox sync` command wrapping rsync over SSH for bidirectional directory synchronization

### Modified Capabilities

(none)

## Impact

- **Code**: New command files in `cmd/pmox/` reusing `resolveSSHTarget`, `buildSSHClient`, and `getOrStartVM` from `ssh.go`
- **Dependencies**: No new Go dependencies — relies on system `scp` and `rsync` binaries (same pattern as `ssh`)
- **CLI surface**: Two new top-level commands registered in `main.go`
