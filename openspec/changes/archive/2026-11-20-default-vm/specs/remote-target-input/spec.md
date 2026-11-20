# Spec Delta

## ADDED Requirements

### Requirement: Targets use the default VM
For `cp`, `sync` and `mount`, a target written without a VM (a bare `:`, `:<path>`, or `mount`'s bare remote path) SHALL resolve to the context's default VM when one is set, without prompting. The interactive `<vm>:<path>` field SHALL open with the default VM filled in and the cursor in the path, and SHALL stay editable so another VM can be chosen. `--pick` SHALL ignore the default.

#### Scenario: Bare colon with a default
- **WHEN** `web1` is the default, three pmox VMs exist, and a user runs `pmox cp ./app.tar :/tmp/`
- **THEN** the file is copied to `web1:/tmp/` with no picker

#### Scenario: Field prefilled
- **WHEN** `web1` is the default and a user runs `pmox mount ./src` on a terminal
- **THEN** the target field opens as `web1:` with the path suggestion, and ↑/↓ still switch VM

#### Scenario: Mount with a bare remote path
- **WHEN** `web1` is the default and a user runs `pmox mount ./src /opt/app`
- **THEN** the mount targets `web1:/opt/app`
