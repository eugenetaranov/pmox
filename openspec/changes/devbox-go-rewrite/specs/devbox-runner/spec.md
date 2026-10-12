## ADDED Requirements

### Requirement: Failure handling
The runner SHALL run items in plan order. An item SHALL fail at its first failing step. Items that need a failed item, and mise items when mise failed, SHALL be skipped with "(skipped: <id> failed)". The run SHALL continue, and SHALL exit 1 listing every failed item.

#### Scenario: Dependent skipped
- **WHEN** `zshcfg` fails and `p10k` is in the plan
- **THEN** `p10k` is reported as skipped because `zshcfg` failed, the other items still run, and the exit code is 1

### Requirement: Batched package installs
All `apt_repo` steps in a plan SHALL be applied before a single `apt-get update`. Packages from all `apt` steps SHALL be installed in one transaction, using the non-interactive options of `devbox-setup-runtime`. When the transaction fails, each package SHALL be retried alone, so the failure is reported against the item that owns it.

#### Scenario: One bad package
- **WHEN** one item's apt package does not exist
- **THEN** only that item fails, and the other apt items are installed

### Requirement: Exact dry run
`devbox-setup --dry-run` SHALL print, per item in plan order, the packages, repositories, commands, files with their targets, and units it would change, and SHALL change nothing on the system.

#### Scenario: Dry run of tmux config
- **WHEN** `devbox-setup --dry-run` runs with `tmuxcfg` picked
- **THEN** the output names `~/.tmux.conf` as a file it would write, and no file is written

### Requirement: Verify
`devbox-setup --verify` SHALL run each saved item's checks without changing anything, print each item as ok or missing, and exit 1 when any is missing.

#### Scenario: Removed tool
- **WHEN** the user ran `mise rm -g starship` after installing it, and runs `devbox-setup --verify`
- **THEN** `starship` is reported missing and the exit code is 1

### Requirement: Log and summary
Every command the runner executes SHALL be appended with its output to `/var/log/devbox-setup.log`. On failure, the terminal SHALL show the last lines of the failing command's output. The run SHALL write `~/devbox-setup-summary.txt`, grouping what it installed by mise, apt, and other sources.

#### Scenario: Failure output
- **WHEN** a step's command fails
- **THEN** the terminal shows its last output lines and the log path
