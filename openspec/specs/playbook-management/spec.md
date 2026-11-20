# playbook-management Specification

## Purpose
Setting up, listing, choosing and updating the tack playbooks under `~/.config/pmox/tack/` that `pmox apply` runs, as commands of their own rather than flags on `apply`.

## Requirements

### Requirement: `pmox playbook init`
`pmox playbook init` SHALL scaffold a starter playbook directory, offering the published roles as a checklist on a terminal and writing the fixed starter otherwise. `pmox playbook init <git-url>` SHALL clone that repository there instead, find its playbooks and set the default, asking which when there are several. An existing directory SHALL be moved aside only after confirmation or with `--yes`.

#### Scenario: Starter
- **WHEN** no playbook directory exists and `pmox playbook init --no-input` runs
- **THEN** the fixed starter playbook is written and named as the default

#### Scenario: From a repository
- **WHEN** a user runs `pmox playbook init https://example.com/team/playbooks.git`
- **THEN** the repository is cloned into the playbook directory and its playbook becomes the default

#### Scenario: Existing directory without confirmation
- **WHEN** a playbook directory exists and `pmox playbook init <url>` runs without a terminal and without `--yes`
- **THEN** pmox exits non-zero and leaves the directory untouched

### Requirement: `pmox playbook list` and `default`
`pmox playbook list` SHALL list the playbooks found in the playbook directory, mark the default, and support `--output json`. `pmox playbook default <name>` SHALL set the default; without a name it SHALL show a picker on a terminal and print the current default otherwise.

#### Scenario: List
- **WHEN** the directory holds `playbook.yaml` and `playbooks/db.yaml` and `playbook.yaml` is the default
- **THEN** `pmox playbook list` shows both names with the default marked

#### Scenario: Set default
- **WHEN** a user runs `pmox playbook default playbooks/db`
- **THEN** a later `pmox apply web1` on a VM with no remembered playbook runs `playbooks/db`

#### Scenario: Unknown name
- **WHEN** a user runs `pmox playbook default nosuch`
- **THEN** pmox exits with a user-input error listing the available playbooks

### Requirement: `pmox playbook update`
`pmox playbook update` SHALL pull the cloned playbook repository and report what changed. When the directory is not a git clone it SHALL exit with a user-input error saying so.

#### Scenario: Pull
- **WHEN** the playbook directory is a clone with new upstream commits and a user runs `pmox playbook update`
- **THEN** the commits are pulled and a one-line summary is printed

#### Scenario: Not a clone
- **WHEN** the directory was scaffolded by `playbook init` with no URL
- **THEN** `pmox playbook update` exits with a user-input error

### Requirement: apply names its argument a playbook
`pmox apply`'s usage, help and errors SHALL call its second argument a playbook: `pmox apply [name|vmid] [playbook]`. `apply --init`, `--default` and `--update` SHALL keep working as deprecated forms of the `playbook` commands (see `cli-command-tree`).

#### Scenario: Usage line
- **WHEN** a user runs `pmox apply --help`
- **THEN** the usage line reads `pmox apply [name|vmid] [playbook] [flags]` and the management flags are not listed

#### Scenario: Old flag
- **WHEN** a user runs `pmox apply --update`
- **THEN** the repository is pulled and stderr shows one line naming `pmox playbook update`
