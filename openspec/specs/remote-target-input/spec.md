# remote-target-input Specification

## Purpose
The interactive fields pmox uses to ask for a remote `<vm>:<path>`
target and a local path, how remote paths are read, and how a missing
destination directory is confirmed and created.

## Requirements
### Requirement: Target field for `<vm>:<path>`

pmox SHALL provide an interactive target field that every command
prompting for a remote `<vm>:<path>` uses.

The field SHALL hold one editable value. The text before the first `:`
is the VM part; the text after it is the path part.

Under the input, the field SHALL list the pmox VMs whose name or VMID
starts with the VM part, showing name, status and IPv4 as `pmox list`
does. While the VM part is empty, it SHALL list all pmox VMs. The list
SHALL show at most a few rows, followed by "… N more".

#### Scenario: Opening with several VMs
- **WHEN** the field opens and the cluster has pmox VMs web1, web2 and db1
- **THEN** all three are listed under an empty input with a greyed suggestion of the first VM and the default path

#### Scenario: Typing narrows the list
- **WHEN** the user types `we`
- **THEN** only web1 and web2 are listed

### Requirement: Greyed default and Tab completion

The field SHALL show the completion it would accept as greyed ghost
text after the cursor.

**Tab** (or → / End at the end of the value) SHALL accept it:
- **Cursor in the VM part:**
  - One VM matches: complete it to `<name>:`, followed by the
    command's default path as ghost text.
  - Several match: extend the VM part to their longest common prefix.
    Each further Tab SHALL cycle through the matches.
- **Cursor in the path part:** complete the next path segment from a
  listing of the remote directory being typed.

Remote listing SHALL use one SSH call per directory with a timeout of
at most 2 seconds, cached for the life of the field. When the VM is
stopped or the listing fails, the field SHALL show a one-line hint and
keep accepting typed input.

#### Scenario: Completing a unique VM
- **WHEN** the value is `db` and only db1 matches, and the user presses Tab
- **THEN** the value becomes `db1:` with the default path shown greyed after it

#### Scenario: Completing a remote directory
- **WHEN** the value is `web1:/opt/a`, `/opt` on web1 contains `app/`, and the user presses Tab
- **THEN** the value becomes `web1:/opt/app/`

#### Scenario: Listing unavailable
- **WHEN** the user presses Tab in the path part of a stopped VM
- **THEN** the value is unchanged and a hint says remote completion is unavailable while the VM is stopped

### Requirement: Picking and editing

**↑/↓** SHALL move a highlight through the VM list and replace the VM
part with the highlighted VM's name, keeping the path part unchanged.

Every other key SHALL edit the value as normal text. The user SHALL be
able to backspace into or retype either part at any time, and to pick
a different VM again afterwards.

**Esc** SHALL return to the previous field, or cancel when this is the
first field. **Ctrl-C** SHALL quit with exit code 130.

#### Scenario: Re-picking a VM keeps the path
- **WHEN** the value is `web1:/srv/data` and the user presses ↓ to highlight web2
- **THEN** the value becomes `web2:/srv/data`

#### Scenario: Editing the VM part by hand
- **WHEN** the value is `web1:/srv/data` and the user deletes `1` and types `2`
- **THEN** the value is `web2:/srv/data` and the list highlights web2

### Requirement: Exactly one pmox VM is never asked for

When the cluster has exactly one pmox VM, the field SHALL NOT ask for a VM:
- It SHALL open with the value `<vm>:`, the cursor in the path
  part, the VM part dimmed, and no VM list.
- If the user backspaces into the VM part, the list SHALL be shown
  again.

Every pmox command that needs a VM SHALL apply the same rule: with
exactly one pmox VM it SHALL use that VM without asking.

#### Scenario: Single VM
- **WHEN** the field opens and web1 is the only pmox VM
- **THEN** the value is `web1:` with the cursor after the colon and the default path greyed

### Requirement: Validation on submit

**Enter** SHALL submit the value. When the path part is empty, the
default path SHALL be used.

Submission SHALL be refused, with an inline error and the value kept,
when the VM part doesn't name exactly one pmox VM by name or VMID.

#### Scenario: Unknown VM
- **WHEN** the user submits `web9:/srv` and no VM is named web9
- **THEN** the field stays open with the error "no pmox VM named web9" and the value unchanged

#### Scenario: Empty path uses the default
- **WHEN** the user submits `web1:` and the default path is `/mnt/src`
- **THEN** the target is `web1:/mnt/src`

### Requirement: Relative remote paths are relative to the home directory

A remote path SHALL be read in one of three ways:
- starting with `/`: absolute
- starting with `~/`: relative to the login user's home directory
- anything else: also relative to the login user's home directory, so
  `project/pmox` means `~/project/pmox`

This SHALL hold everywhere a remote path is used:
- the target field
- explicit `<vm>:<path>` arguments to `mount`, `cp` and `sync`
- remote Tab completion
- the missing-directory check and its confirmation, which SHALL name
  the resolved `~/…` path

#### Scenario: Relative path in the field
- **WHEN** the user submits `web1:project/pmox`
- **THEN** the target is `web1:~/project/pmox`

#### Scenario: Relative path as an argument
- **WHEN** `pmox sync . web1:project/pmox` is invoked
- **THEN** rsync's destination is the login user's `~/project/pmox`, and a missing directory is confirmed as "Create ~/project/pmox on web1?"

#### Scenario: Completing a relative path
- **WHEN** the value is `web1:pro` and the user's home on web1 contains `project/`, and the user presses Tab
- **THEN** the value becomes `web1:project/`

### Requirement: Local path field

pmox SHALL provide a local path field with a greyed default (`.`
unless the command says otherwise). Tab SHALL complete local path
segments, offering only directories when the command needs a directory
(`mount`). Enter on an empty value SHALL use the default.

#### Scenario: Accepting the default
- **WHEN** the local field opens and the user presses Enter
- **THEN** the local path is `.`

#### Scenario: Directory completion
- **WHEN** the value is `./sr`, `./src/` and `./srv.txt` exist, and the field is for `mount`
- **THEN** Tab completes to `./src/`

### Requirement: No interactive fields without a terminal

The target and local path fields SHALL appear only when stdin and
stderr are terminals and input is allowed. With `--no-input`,
`PMOX_NO_INPUT`, `--output json`, or no terminal, a command missing
these arguments SHALL fail with its existing usage error.

#### Scenario: Script without arguments
- **WHEN** `pmox mount` runs with stdin not a terminal
- **THEN** it exits with the usage error and no field is shown

### Requirement: Missing destination directory is confirmed, then created

Before transferring, `mount`, `cp` and `sync` SHALL check that the
destination directory exists:
- the destination itself, when the destination ends in `/`, or for
  `mount` and `sync` when the source is a directory;
- otherwise, the destination's parent. For `cp -r` this keeps scp's
  meaning: copying `src` to a missing `dest` creates `dest`, not
  `dest/src`.

The remote side SHALL be checked over SSH; the local side on the local
filesystem.

**When the directory is missing, interactively**, the command SHALL ask
"Create <path> on <vm>?" (or "Create <path>?" for a local path),
defaulting to Yes.
- On Yes it SHALL create the directory, including parents. A remote
  directory SHALL be owned by the login user, using non-interactive
  `sudo` when the parent isn't writable.
- On No it SHALL stop with nothing transferred.

**Without a terminal**, it SHALL fail with an error naming the path
and `--mkdir`. With `--mkdir`, it SHALL create the directory without
asking.

When creation fails, the error SHALL name the path and suggest a
writable location.

#### Scenario: Sync into a missing directory
- **WHEN** a user runs `pmox sync . web1:~/project/pmox` and `~/project` doesn't exist on web1
- **THEN** pmox asks "Create ~/project/pmox on web1?" and, on Yes, creates it and syncs

#### Scenario: Mount under root-owned /mnt
- **WHEN** a user mounts `./src` to `web1:/mnt/src`, which doesn't exist, and confirms
- **THEN** `/mnt/src` is created with sudo, owned by the login user, and the first sync succeeds

#### Scenario: Script without --mkdir
- **WHEN** `pmox cp -r ./cfg web1:/opt/new/` runs without a terminal and `/opt/new` doesn't exist
- **THEN** it fails before running scp, with an error naming `/opt/new` and `--mkdir`

