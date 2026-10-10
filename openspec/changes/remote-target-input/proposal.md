## Why

Bare `pmox mount` asks two plain text prompts ("Local path to sync:",
"Remote target ([name|vmid:]path):"). Nothing is suggested, so the user
has to remember the VM's name and type the whole `vm:path` by hand, with
no completion and no list to choose from. `pmox cp` and `pmox sync`
use a separate VM picker followed by bare path prompts, and bare
`pmox umount` asks for a VM rather than showing the mounts that are
actually running.

## What Changes

- A new interactive **target field** for `<vm>:<path>` arguments:
  - It lists the pmox VMs (name, status, IP) under the input.
  - It shows a greyed suggestion the user can accept, for example
    `web1:/mnt/src`.
  - **Tab** completes the VM name, and then remote directories.
  - **↑/↓** picks a VM from the list and keeps the typed path.
  - The whole value stays editable: backspace into the path or the VM
    name, retype, or pick again.
- A **local path field** with a greyed default (`.`) and Tab completion
  of local paths (directories only for `mount`).
- `pmox mount`, run interactively with missing arguments, uses both
  fields on one screen.
  - The default remote path is `/mnt/<local directory name>`.
  - `pmox mount ./src` (local path only) opens the target field
    instead of failing.
- **Missing destination directory:** `mount`, `cp` and `sync` check
  that it exists before transferring.
  - On a terminal they ask "Create <path> on <vm>?" and create it,
    owned by the login user. They use `sudo` when the parent isn't
    writable, such as `/mnt`.
  - Scripts pass `--mkdir`.
  - Today this fails inside rsync with
    `change_dir … No such file or directory`.
- **Exactly one pmox VM is never asked for**, in any command or field.
  The target field opens as `<vm>:` with the cursor in the path. This
  unifies today's mix: `sync` and the picker auto-select, but `mount`'s
  plain prompt doesn't.
- `pmox sync` passes `rsync -a` by default, so directories are copied
  instead of `skipping directory .`. `--no-archive` opts out.
- `pmox cp` and `pmox sync`, run interactively with no arguments,
  replace the VM picker and bare path prompts with the target field and
  the local path field, after the direction choice.
- Bare `pmox umount` lists the running mounts (`local → vm:path`) and
  stops the ones chosen. With nothing running it reports success.
- No change to explicit arguments, scripts, or non-interactive use:
  missing arguments stay a usage error.

## Capabilities

### New Capabilities
- `remote-target-input`: the interactive `<vm>:<path>` target field
  and local path field: suggestions, completion, VM list, editing
  keys, validation, and the non-interactive fallback.

### Modified Capabilities
- `mount-command`:
  - interactive entry uses the new fields
  - one-argument form
  - `/mnt/<dir>` default
  - missing-directory confirmation and `--mkdir`
  - bare `umount` picks from running mounts
  - umount of a mount that isn't running is success, in line with
    `cli-progress-feedback`
- `cp-command`: the interactive no-argument flow uses the target field
  and the local path field; adds missing-directory confirmation and
  `--mkdir`.
- `sync-command`: same as `cp-command`, plus `-a` by default and
  `--mkdir`.

## Impact

- New `internal/tui/target` package: a bubbletea model built on the
  bubbles `textinput`, which already supports ghost-text suggestions.
- `cmd/pmox/mount.go` (`resolveMountArgs`, `mountResolveDestFn`, umount
  with no arguments), `cmd/pmox/cp.go` (`resolveCpSyncArgs`), and a
  remote `mkdir` step before the first rsync.
- Remote path completion runs one SSH directory listing per completed
  directory, cached for the session. It is skipped for stopped VMs.
- README and llms.txt sections for mount, cp and sync.
