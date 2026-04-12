## Context

pmox already has `shell` and `exec` commands that resolve a VM name/VMID to an SSH target (IP, user, identity key) and delegate to the system `ssh` binary. File transfer is the natural next step — users currently have to manually run `scp` or `rsync` with the right connection details, which defeats the multipass-style UX.

The existing `ssh.go` contains all the SSH target resolution logic: `buildSSHClient`, `resolveSSHTarget`, `getOrStartVM`, `resolveIdentityKey`, and `buildSSHArgs`. Both new commands need the same resolution but construct different command-line invocations.

## Goals / Non-Goals

**Goals:**
- `pmox cp` wraps `scp` for single-file and recursive copy in both directions (local→VM, VM→local)
- `pmox sync` wraps `rsync` over SSH for efficient directory synchronization in both directions
- Both commands reuse existing SSH target resolution (auto-start, IP discovery, tag check, identity key)
- UX follows the `<name>:<path>` convention familiar from scp/multipass

**Non-Goals:**
- VM-to-VM copy (both endpoints referencing different VMs)
- Glob expansion or multi-file arguments beyond what scp/rsync handle natively
- Built-in progress bars — scp/rsync already show progress on TTYs
- Background/daemon sync modes

## Decisions

### 1. Argument syntax: `<name>:<path>` convention

Use `<name>:<path>` to identify remote paths, matching scp and multipass conventions. Exactly one of source or destination must contain a `:` — this determines the direction. The part before `:` is a VM name or VMID resolved through the existing `vm.Resolve` path.

**Alternative considered**: Separate `push`/`pull` subcommands. Rejected because `<name>:<path>` is universally understood and avoids doubling the command surface.

### 2. Delegate to system `scp` and `rsync` binaries

Same pattern as `shell`/`exec` — `exec.LookPath` the binary, build args, run it. This avoids reimplementing transfer protocols, inherits compression/progress/bandwidth-limiting flags, and means updates to scp/rsync benefit pmox users automatically.

**Alternative considered**: Using `golang.org/x/crypto/ssh` + `github.com/pkg/sftp` for a pure-Go scp. Rejected because it would duplicate significant complexity for no user-visible benefit, and `rsync` has no pure-Go equivalent.

### 3. Pass-through extra flags via `--`

Both commands accept `-- <extra-flags>` that are appended verbatim to the underlying scp/rsync invocation. This lets advanced users pass `-r`, `-z`, `--delete`, etc. without pmox needing to mirror every option.

For `cp`, `-r` (recursive) is also exposed as a first-class `--recursive` / `-r` flag for discoverability.

### 4. Inject SSH options via `-o` flags (cp) and `-e` flag (sync)

`scp` accepts `-o` options directly. `rsync` uses `-e 'ssh <opts>'` to customize the SSH transport. Both get the same `StrictHostKeyChecking=no`, `UserKnownHostsFile=/dev/null`, and optional `-i <key>` that `shell`/`exec` use.

### 5. Both commands live in a new `cp.go` file

Both `cp` and `sync` are small enough to share a single file. They share the argument parsing logic (splitting `<name>:<path>`), SSH flag setup, and target resolution. Following the tack convention, they don't warrant separate files.

## Risks / Trade-offs

- **[Risk] `scp` deprecation**: OpenSSH has deprecated the legacy scp protocol in favor of sftp-based transfer. Modern `scp` (OpenSSH 9.0+) uses SFTP internally, so this is transparent. → Mitigation: No action needed; the `scp` binary continues to work.
- **[Risk] `rsync` not installed**: Unlike `ssh` and `scp`, `rsync` is not guaranteed on all systems. → Mitigation: Clear error message when `rsync` is not found on PATH.
- **[Trade-off] No pure-Go fallback**: If system binaries are missing, the commands fail. This matches the existing pattern for `shell`/`exec` and keeps the codebase simple.
