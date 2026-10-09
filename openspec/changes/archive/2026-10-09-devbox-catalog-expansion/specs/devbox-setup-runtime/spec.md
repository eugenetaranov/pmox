## ADDED Requirements

### Requirement: pmox-owned config files
devbox-setup's own config files SHALL live in `internal/bootstrap/conf/`, embedded alongside `assets/`, and SHALL land on the VM under `/usr/local/share/devbox-setup/conf/`. `internal/bootstrap/assets/` SHALL contain only files copied by `task bootstrap:sync`.

#### Scenario: Sync keeps pmox config
- **WHEN** `task bootstrap:sync` runs
- **THEN** every file under `internal/bootstrap/conf/` is unchanged

#### Scenario: Conf files reach the VM
- **WHEN** pmox builds cloud-init for a launch
- **THEN** `write_files` contains each `conf/` file under `/usr/local/share/devbox-setup/conf/`

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
`load_answers` SHALL map ids and variables that this change renames or splits, so that `--yes` reproduces the previous selection:
- `editors` becomes `editors neovim`
- `A_WIRE_CLAUDE=yes` becomes `A_WIRE_AGENTS=yes`

#### Scenario: Old answers file
- **WHEN** an answers file from before this change has `A_PICKS="editors"` and `A_WIRE_CLAUDE=yes`, and `devbox-setup --yes` runs
- **THEN** the plan includes `editors` and `neovim`, and mcpjungle is wired into the picked agents
