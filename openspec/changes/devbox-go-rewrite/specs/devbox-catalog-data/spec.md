## ADDED Requirements

### Requirement: Catalog as embedded TOML
The catalog SHALL be TOML files embedded in the devbox-setup binary, one per tab. Each item SHALL have an `id`, `tab`, `label`, optional `needs`, an optional `source` text shown in the picker, and a non-empty list of `steps`. Item order in the picker and the install order SHALL follow file order, tab by tab.

#### Scenario: Install order
- **WHEN** the user picks `p10k` and `cli`
- **THEN** the plan lists the items in catalog order, with `zsh` and `zshcfg` added before `p10k`

### Requirement: Catalog validation
devbox-setup SHALL refuse to start, naming the item, when the catalog has a duplicate id, a `needs` entry that is not an id, a needs cycle, an unknown step type, or a step that refers to a file missing from the binary. The same checks SHALL run in the test suite.

#### Scenario: Unknown need
- **WHEN** an item has `needs = ["nope"]`
- **THEN** the catalog test fails naming the item and `nope`

### Requirement: Step types
The catalog SHALL support these declarative step types: `apt`, `apt_repo`, `mise`, `file`, `template`, `line`, `git_clone`, `git_config`, `link`, `systemctl`, `sysctl`, `user_shell`, `run` and `note`. It SHALL also support `go:<name>` for steps written in Go. A `run` step SHALL declare `creates`, `check` or `on_change` (it runs only after an earlier step of its item changed something), so that a re-run of a completed item does nothing. A `note` step SHALL show its text after the run when its condition holds, and SHALL never count as a change.

#### Scenario: Re-run is a no-op
- **WHEN** `sudo devbox-setup --yes` runs twice with the same answers
- **THEN** the second run changes no files and reports every item as already done

#### Scenario: A line once
- **WHEN** a `line` step adds the shell drop-in to `~/.bashrc` on two runs
- **THEN** `~/.bashrc` has the line once

#### Scenario: File backup
- **WHEN** a `file` step with `keep_orig = true` writes `~/.tmux.conf` and the user already has one
- **THEN** the old file is kept as `~/.tmux.conf.orig`, and a later run does not overwrite the `.orig`

### Requirement: Local catalog additions
devbox-setup SHALL load `/etc/devbox-setup/catalog.d/*.toml` after the embedded catalog. An item there with a new id SHALL be added to its tab. An item with an existing id SHALL replace that item. The same validation applies.

#### Scenario: Local item
- **WHEN** `/etc/devbox-setup/catalog.d/team.toml` defines an item `teamtools` on the `dev` tab
- **THEN** the Dev tools tab lists it and search finds it

### Requirement: Credential step type
The catalog SHALL support a `cred` step. It declares a target file, a mode, keys (each with name, whether it is secret, where to get it and whether it is required), an optional apply command and an optional logged-in check. It SHALL behave as the `devbox-credentials` capability specifies: ask or skip, write at the mode, and add a note when values are empty. It SHALL never store values outside the target file.

#### Scenario: Ported item
- **WHEN** `npmrc` is defined as a `cred` step
- **THEN** its behaviour matches the `devbox-credentials` scenarios for `npmrc`

### Requirement: Downloads are pinned or declared unverified
Every `apt_repo` step SHALL declare `key_fingerprint`. The key SHALL be checked against it before it is installed, and a mismatch fails the item. Every step that downloads a file SHALL declare `sha256` or `unverified = "<reason>"`. Catalog validation SHALL reject a download with neither.

#### Scenario: Missing pin
- **WHEN** a catalog item downloads a release binary without `sha256` or `unverified`
- **THEN** catalog validation fails naming the item

#### Scenario: Wrong apt key
- **WHEN** a vendor apt key's fingerprint does not match
- **THEN** the item fails naming the repository, and the key is not installed
