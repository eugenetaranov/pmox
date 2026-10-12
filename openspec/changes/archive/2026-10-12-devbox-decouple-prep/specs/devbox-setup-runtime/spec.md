## MODIFIED Requirements

### Requirement: pmox-owned config files
devbox-setup's own config files, including `zshrc.tmpl` and `vimrc`, SHALL live in `internal/bootstrap/conf/` and SHALL land on the VM under `/usr/local/share/devbox-setup/conf/`. `internal/bootstrap/assets/` SHALL contain only files copied by `task bootstrap:sync`: `mcp-sync`, `mcp-catalog/*.spec` and `SOURCE`.

#### Scenario: Sync keeps pmox config
- **WHEN** `task bootstrap:sync` runs
- **THEN** every file under `internal/bootstrap/conf/` is unchanged, and `assets/` has no `zshrc.tmpl` or `vimrc`

#### Scenario: Conf files reach the VM
- **WHEN** pmox installs devbox-setup's files on a VM
- **THEN** each `conf/` file is under `/usr/local/share/devbox-setup/conf/`

### Requirement: Saved-answer migration
Saved answers SHALL be read from `/var/lib/devbox-setup/answers.json`, a JSON object with `"version": 1`. Neither that file nor a legacy one SHALL ever be executed or sourced. When only the legacy shell-format `answers` file exists, devbox-setup SHALL decode its `A_*=<%q value>` lines for known variables, ignore any other line with a warning, write `answers.json` (mode 0600), and rename the legacy file to `answers.old`. On read, ids and variables that were renamed or split SHALL be mapped, so that `--yes` reproduces the previous selection:
- `editors` becomes `editors neovim`
- `shell` becomes `zsh zshcfg p10k`
- `kubectl`, `kubectx`, `helm` and `k9s` become `k8s`
- `devbox` becomes `essentials cli editors tmux zellij`
- `typescript` becomes `node`, and `kotlin` becomes `java`
- `chrony` is dropped, with a note that pmox now sets the clock up at launch
- `A_WIRE_CLAUDE=yes` becomes `wire_agents: true`

#### Scenario: Old answers file
- **WHEN** an answers file from before this change has `A_PICKS="editors"` and `A_WIRE_CLAUDE=yes`, and `devbox-setup --yes` runs
- **THEN** the plan includes `editors` and `neovim`, mcpjungle is wired into the picked agents, `answers.json` exists, and `answers` is renamed to `answers.old`

#### Scenario: Tampered legacy file
- **WHEN** the legacy file contains `A_PICKS=cli; touch /tmp/pwned` or a line `touch /tmp/pwned`
- **THEN** `/tmp/pwned` is not created, the unknown line is reported, and no command from the file runs

#### Scenario: Round trip
- **WHEN** a legacy file written by the previous `save_answers` (values with spaces, quotes and non-ASCII names) is converted
- **THEN** every `A_*` value read from `answers.json` equals the value the old file set when sourced

## ADDED Requirements

### Requirement: Checksum-pinned downloads
Release binaries that devbox-setup downloads (gum) SHALL be verified against a SHA-256 pinned in the script for each supported architecture before they are extracted or installed. A mismatch SHALL stop devbox-setup with an error naming the file, and SHALL leave nothing installed.

#### Scenario: Corrupted download
- **WHEN** the gum tarball downloaded does not match the pinned SHA-256
- **THEN** devbox-setup exits non-zero naming gum's tarball, and `/usr/local/bin/gum` is not changed
