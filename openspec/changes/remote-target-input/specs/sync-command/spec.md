## MODIFIED Requirements

### Requirement: `pmox sync` command

The CLI SHALL expose `pmox sync <source> <destination>` which synchronizes files between the local host and a pmox-managed VM using the system `rsync` binary over SSH. Exactly one of source or destination SHALL use the `<name|vmid>:<path>` syntax to identify the remote side.

The command SHALL accept `--user` / `-u` (default `"pmox"`), `--identity` / `-i`, and `--force` flags with identical behavior to `pmox shell`.

The command SHALL construct the rsync `-e` flag to pass SSH options: `-e 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key>'`.

The command SHALL pass `-a` to rsync by default, ahead of any flags given after `--`, so directories are copied recursively. `--no-archive` SHALL drop it.

Additional flags MAY be passed after `--` and SHALL be appended verbatim to the rsync invocation. This allows users to pass flags like `--delete`, `-z`, `--exclude`, etc.

The command SHALL accept `--mkdir` and SHALL confirm and create a missing destination directory as defined in `remote-target-input`.

#### Scenario: Sync local directory to VM
- **WHEN** `pmox sync ./src/ web1:/opt/app/` is invoked against a running pmox-tagged VM with IP `192.168.1.10`
- **THEN** the command SHALL run `rsync -a -e 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key>' ./src/ pmox@192.168.1.10:/opt/app/`
- **AND** pmox SHALL exit with rsync's exit code

#### Scenario: Sync from VM to local
- **WHEN** `pmox sync web1:/var/log/ ./logs/` is invoked
- **THEN** the command SHALL run rsync with the remote side as source

#### Scenario: Extra flags via `--`
- **WHEN** `pmox sync ./src/ web1:/opt/app/ -- --delete --exclude .git` is invoked
- **THEN** the command SHALL append `--delete --exclude .git` to the rsync arguments

#### Scenario: Custom user and identity
- **WHEN** `pmox sync --user ubuntu --identity ~/.ssh/custom ./src/ web1:/opt/` is invoked
- **THEN** the rsync `-e` flag SHALL reference `-i ~/.ssh/custom` and the remote path SHALL use `ubuntu@<ip>`

#### Scenario: Directory source is copied
- **WHEN** `pmox sync . web1:~/project/pmox` is invoked and the destination exists
- **THEN** rsync runs with `-a` and copies the directory's contents instead of printing `skipping directory .`

#### Scenario: Archive mode off
- **WHEN** `pmox sync --no-archive ./f web1:/tmp/` is invoked
- **THEN** rsync runs without `-a`

## ADDED Requirements

### Requirement: Interactive transfer prompts

When `pmox sync` is invoked with no arguments on a terminal, it SHALL ask for the direction (upload or download). It SHALL then use the fields defined in `remote-target-input`, in direction order:
- upload: the local path field, then the target field
- download: the target field, then the local path field

The local path field SHALL accept files and directories. Without a terminal, no arguments SHALL remain a usage error.

#### Scenario: Interactive upload with one VM
- **WHEN** `pmox sync` is invoked with no arguments on a terminal and web1 is the only pmox VM, and the user picks upload
- **THEN** the local field opens with `.` greyed, then the target field opens as `web1:` without asking for a VM
