## Context

`resolveMountArgs` (`cmd/pmox/mount.go`) prompts with `promptRequired`,
a plain line read: there are no suggestions, no completion and no list.
A bare remote path goes to `mountResolveDestFn`, which calls the shared
VM picker (`vm.Pick`). `resolveCpSyncArgs` (`cmd/pmox/cp.go`) asks for
a direction, then runs the VM picker, then asks for two bare paths. Bare
`umount` picks a VM and stops all of its mounts, even when the mounts
that are running are already known locally (`mount.Find` /
`mountRecordsForVM` read the state dir).

The TUI stack is bubbletea v1.3, bubbles v0.21 and huh v1. The bubbles
`textinput` already supports `SetSuggestions` with ghost text and Tab
to accept. A huh `Input` can't render a live VM list under the field,
and can't rewrite part of its value when an arrow key is pressed.

## Goals / Non-Goals

**Goals:**
- Never make the user remember or fully type a VM name or a path.
- One editable value, `<vm>:<path>`: whatever is suggested or picked
  can still be changed with plain editing keys.
- Reuse the same field in mount, cp and sync. umount picks from the
  mounts that are running.
- Explicit arguments, scripts and non-interactive use behave exactly as
  they do today.

**Non-Goals:**
- Fuzzy matching. Completion is by prefix, which is predictable with
  Tab.
- Remote completion through the guest agent, which has no directory
  listing.
- Changing `shell`, `exec` or `apply`. They take a VM only, and the
  existing picker already serves them.

## Decisions

### D1. A custom bubbletea model, `internal/tui/target`

**Choice:** wrap the bubbles `textinput` and render the VM list under
it in the same model.

**Why:** that gives ghost text and Tab for free, plus full control of
↑/↓ and the list.

**Alternatives:**
- huh `Input` with `Suggestions`: it can't show the list or replace
  only the VM part of the value.
- A huh `Select` followed by an `Input`: that's what cp/sync do today,
  and it is the two-step flow the user wants gone.

### D2. One value, split at the first `:`

The field holds a single string.
- The **VM part** is the text before the first `:`.
- The **path part** is the text after it.
- With no `:`, the whole value is the VM part.

The list under the field shows the pmox VMs whose name or VMID starts
with the VM part. While the VM part is empty, all of them are shown.
Each row is name, status and IP, as in `pmox list`.

| Key | Effect |
|---|---|
| Tab, cursor in the VM part, one VM matches | complete it to `<name>:` and fill the default path as ghost text |
| Tab, several VMs match | extend to their longest common prefix; Tab again cycles through them |
| Tab, cursor in the path part | complete the next remote path segment (D4) |
| ↑/↓ | move the highlight in the list and replace the VM part with that VM's name; the typed path is kept |
| → or End, at the end of the value | accept the ghost text, like Tab |
| Enter | submit; with an empty path part, the default path is used |
| Esc | back to the previous field (or cancel on the first field) |
| Ctrl-C | quit, exit 130 |

Everything else is ordinary text editing, so the user can backspace
into either part at any time.

### D3. Default remote path: `/mnt/<local directory name>`

**Choice:** the default is `/mnt/<local dir>`, where `<local dir>` is
the base name of the absolute local path. `.` in `~/code/src` gives
`/mnt/src`. The local path field defaults to `.`.

**Why:** it's what the user asked for. It is predictable, and it keeps
synced trees out of the home directory.

**Alternative:** `~/<dir>`. It needs no sudo, but the user picked
`/mnt`. The default is one constant, so it is easy to change.

### D4. Remote path completion over SSH

**How it works:** on Tab in the path part, pmox lists the directory
being typed with one SSH call (`ls -1pA -- <dir>`). It reuses the
target's SSH user, key and host-key options, with a short timeout.
Results are cached per `(vm, dir)` for the life of the field.

**When it's skipped:** the VM is stopped, the listing fails, or it
takes longer than 2s. A short "no completion: <reason>" hint is shown
and typing still works.

**Why:** it's the only reliable listing; the guest agent can't list
directories. One cached call per directory keeps Tab responsive.

### D5. Missing destination directory: confirm, then create

`mount`, `cp` and `sync` check that the destination directory exists
before transferring.

**Which directory is checked:**
- The destination itself, when it ends in `/`, or for `mount` and
  `sync` when the source is a directory.
- Otherwise: the destination's parent. That includes `cp -r` of a
  directory to a path without a trailing `/`, where pre-creating the
  destination would change scp's result from `dest` to `dest/src`.

**How the check runs:**
- Remote side: one SSH `test -d`.
- Local side (downloads): `os.Stat`.

**If the directory is missing:**
- **Interactive:** ask "Create <path> on <vm>? [Y/n]" (default Yes).
  - Yes: create it. Remote:
    ```
    mkdir -p -- P 2>/dev/null || sudo -n install -d -o "$(id -un)" -g "$(id -gn)" -- P
    ```
    so a root-owned parent such as `/mnt` works, and the directory ends
    up owned by the login user. Local: `os.MkdirAll`.
  - No: stop with nothing transferred (exit 130, like any declined
    prompt).
- **Non-interactive:** fail with an error naming the path and the
  `--mkdir` flag, unless `--mkdir` is passed. `--mkdir` creates the
  directory without asking.

**Why:** rsync and scp can't create missing parents. The user hit
this with `pmox sync . ~/project/pmox`: `change_dir … No such file or
directory`. Asking first avoids silently creating a directory from a
mistyped path.

**On failure** (for example `sudo -n` needs a password): stop with an
error that names the path and suggests a writable one.

### D6. Exactly one pmox VM: never ask for it

**Choice:** this is the shared rule for every command and field. With
exactly one pmox VM, its name is filled in and the VM step is skipped.
- **Target field:** it opens as `<vm>:` with the cursor in the path
  part. The VM list is hidden and the VM part is shown dimmed. The
  user can still backspace into the VM part, which re-opens the list
  (for example, if they meant a VM they're about to launch).
- **Commands that only pick a VM:** they keep the picker's silent
  auto-select.

**Why:** today `sync` and the picker auto-select, but `mount`'s plain
prompt doesn't. This makes it the same everywhere.

### D7. sync copies directories by default

`pmox sync` runs `rsync -a` by default, ahead of any flags given after
`--`.

**Why:** without `-a` (or `-r`), rsync prints `skipping directory .`
and copies nothing when the source is a directory. That's the other
half of the reported failure. `-a` is also correct for a single file.

**Opting out:** `--no-archive` drops it for users who need full
control.

### D8. Where each field appears

**mount**
- With no arguments, the screen has two fields: Local (default `.`),
  then Target.
- With one argument, only the Target field is shown.
- An explicit `<vm>:<path>` argument never opens a field.
- A bare remote-path argument keeps today's behaviour: the VM picker
  only.

**cp and sync**
- With no arguments, the screen shows the direction choice, then the
  field order follows the direction:
  - upload: Local, then Target
  - download: Target, then Local
- The local field accepts files as well as directories.

**umount**
- With no arguments: a multi-select of the running mounts, read from
  the local state dir with no cluster call.
- With nothing running: "✓ No active mounts" and exit 0.

**Non-interactive** (no TTY, `--no-input`, `--output json`): the
existing missing-argument errors.

### D9. Validation on Enter

Enter is rejected, with an inline error and the value kept, when the
VM part doesn't name exactly one pmox VM.

**Paths need no validation.** Anything that starts with neither `/`
nor `~/` is taken as relative to the login user's home: `project/pmox`
means `~/project/pmox`. That's also how scp and rsync read a relative
remote path, so explicit arguments already behave this way.

pmox makes the rule explicit in three places:
- the missing-directory prompt, which shows the `~/…` form
- remote completion, which lists relative to `$HOME`
- `mount`'s SSH `mkdir`, which runs from the home directory

(Exactly one VM: see D6.)

## Risks / Trade-offs

- [`sudo -n` is unavailable or needs a password on a custom image] →
  The error names the path and suggests a writable alternative such as
  `~/<dir>`. Nothing is half-created.
- [Adding `-a` to sync changes what existing scripts copy] → It only
  adds recursion and attribute preservation, which every
  directory-sync caller needed anyway. `--no-archive` restores the old
  argument list exactly.
- [SSH completion is slow on a cold VM] → 2s timeout, results cached,
  completion skipped silently after a failure. It never blocks typing.
- [Tab cycling surprises users who expect a shell-style list] → The
  list under the field already shows every match. Tab extends to the
  common prefix first, as shells do.
- [Narrow terminals] → The list caps at a few rows plus "… N more".
  Long paths scroll inside the input.

## Migration Plan

Mostly additive for interactive use. These behaviour changes land
with this change:
- bare `umount` picks mounts instead of a VM
- `sync` adds `-a` by default
- a missing destination directory now prompts, or errors without
  `--mkdir`, instead of failing inside rsync or scp The README and llms.txt are updated in the
same commit.

## Open Questions

- Is `/mnt/<dir>` the right default, or should it be configurable in
  `config.yaml` (for example `mount_default_dir`)? This proposal fixes
  it at `/mnt` and leaves configuration for a follow-up.
