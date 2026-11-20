# Spec Delta

## ADDED Requirements

### Requirement: mount list shows a one-word state
The STATE column of `pmox mount list` SHALL hold one word: `running` or `stale`. When any mount is stale, pmox SHALL print one line below the table naming the command that restarts stale mounts and the command that clears them. `--output json` SHALL carry the state as a field and no hint.

#### Scenario: A stale mount
- **WHEN** one mount's process is not running and a user runs `pmox mount list`
- **THEN** its STATE cell reads `stale`
- **AND** one hint line follows the table

#### Scenario: All running
- **WHEN** every mount is running
- **THEN** no hint line is printed
