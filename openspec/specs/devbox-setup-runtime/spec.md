## Purpose

How devbox-setup runs on a VM: non-interactive apt, safe key downloads,
secrets kept out of argv, visible warnings, saved-answer migration, and
pmox-owned config files kept apart from the synced tack-roles assets.
## Requirements
### Requirement: pmox-owned config files
devbox-setup's own config files, including `zshrc.tmpl` and `vimrc`, SHALL live in `internal/bootstrap/conf/` and SHALL land on the VM under `/usr/local/share/devbox-setup/conf/`. `internal/bootstrap/assets/` SHALL contain only files copied by `task bootstrap:sync`: `mcp-sync`, `mcp-catalog/*.spec` and `SOURCE`.

#### Scenario: Sync keeps pmox config
- **WHEN** `task bootstrap:sync` runs
- **THEN** every file under `internal/bootstrap/conf/` is unchanged, and `assets/` has no `zshrc.tmpl` or `vimrc`

#### Scenario: Conf files reach the VM
- **WHEN** pmox installs devbox-setup's files on a VM
- **THEN** each `conf/` file is under `/usr/local/share/devbox-setup/conf/`

### Requirement: Non-interactive apt
Every apt-get call that devbox-setup makes SHALL keep existing config files without prompting (`--force-confdef --force-confold`) and SHALL NOT restart services during the run (`NEEDRESTART_MODE=l`). `/etc/apt/apt.conf.d/90devbox-setup` SHALL carry the same dpkg options, so vendor install scripts inherit them.

#### Scenario: Modified conffile
- **WHEN** a package being installed ships a conffile that the user has modified
- **THEN** the install keeps the user's file and the step succeeds

#### Scenario: Docker not restarted mid-run
- **WHEN** a later apt step upgrades a library that docker uses, while mcpjungle containers are running
- **THEN** docker is not restarted by needrestart during the run

### Requirement: Mise PATH line in bash
`install_mise` SHALL ensure that the mise shims PATH line is the first line of the user's `.bashrc`, including when `.bashrc` is empty or missing.

#### Scenario: Empty bashrc
- **WHEN** the user has no `.bashrc` and mise is installed
- **THEN** `.bashrc` exists and its first line is the mise shims PATH export

### Requirement: Secrets kept out of argv
devbox-setup SHALL NOT pass secrets as command-line arguments. The Tailscale auth key SHALL be written to a 0600 temporary file, passed as `--auth-key=file:<path>`, and removed afterwards.

#### Scenario: Tailscale key
- **WHEN** `TS_AUTHKEY` is set and tailscale is picked
- **THEN** no process argv contains the key, and the temporary key file is removed after `tailscale up`

### Requirement: Safe key downloads
Downloads of apt signing keys SHALL go to a temporary file, be checked as non-empty, and then be moved into `/etc/apt/keyrings`. A failed download SHALL leave no file behind.

#### Scenario: Interrupted key download
- **WHEN** the download of a signing key fails partway through
- **THEN** no partial file exists in `/etc/apt/keyrings`, and the next run downloads it again

### Requirement: Visible readiness timeouts
When a service that devbox-setup waits on (mcpjungle) is not ready within its timeout, devbox-setup SHALL print a warning naming the service and the log command to check.

#### Scenario: mcpjungle slow to start
- **WHEN** mcpjungle does not answer on its port within 60 seconds
- **THEN** a warning mentions mcpjungle and `journalctl -u mcpjungle`, before mcp-sync runs

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

### Requirement: Checksum-pinned downloads
Release binaries that devbox-setup downloads (gum) SHALL be verified against a SHA-256 pinned in the script for each supported architecture before they are extracted or installed. A mismatch SHALL stop devbox-setup with an error naming the file, and SHALL leave nothing installed.

#### Scenario: Corrupted download
- **WHEN** the gum tarball downloaded does not match the pinned SHA-256
- **THEN** devbox-setup exits non-zero naming gum's tarball, and `/usr/local/bin/gum` is not changed

