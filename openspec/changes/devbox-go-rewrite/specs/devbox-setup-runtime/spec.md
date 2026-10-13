## MODIFIED Requirements

### Requirement: pmox-owned config files
devbox-setup's own config files SHALL live in its source tree and SHALL be embedded in the binary. `task bootstrap:sync` SHALL copy only the tack-roles files (`mcp-sync`, `mcp-catalog/*.spec`, `SOURCE`) into their own directory, and SHALL NOT change devbox-setup's config files.

#### Scenario: Sync keeps pmox config
- **WHEN** `task bootstrap:sync` runs
- **THEN** only the tack-roles files change

#### Scenario: Conf files reach the VM
- **WHEN** an item writes one of devbox-setup's config files
- **THEN** its contents come from the binary, with no share directory on the VM

### Requirement: Checksum-pinned downloads
Release binaries and vendor install scripts that steps download SHALL be verified against a SHA-256 pinned in the catalog when the vendor publishes a stable artifact. A mismatch SHALL fail that item, naming the file, and SHALL leave nothing installed. Downloads without a pin SHALL be listed in a test, so new ones are a reviewed choice.

#### Scenario: Corrupted download
- **WHEN** a pinned download does not match its SHA-256
- **THEN** the item fails naming the file, and its target is not changed

## ADDED Requirements

### Requirement: Local override files
The shell, tmux and vim config devbox-setup writes SHALL source, last and only if present:
- `~/.zshrc.local`
- `~/.config/devbox/local.sh` (zsh and bash)
- `~/.tmux.local.conf`
- `~/.vimrc.local`

devbox-setup SHALL NOT create or change these files.

#### Scenario: Export survives a re-run
- **WHEN** the user puts `export KUBECONFIG=~/.kube/work` in `~/.config/devbox/local.sh` and runs `sudo devbox-setup --yes`
- **THEN** a new login shell has `KUBECONFIG=~/.kube/work`

### Requirement: Per-user state
System items' answers, the users set up and their MCPJungle ports SHALL be kept in root-only `/var/lib/devbox-setup/system.json`, and each user's items and settings in `~/.config/devbox/answers.json` in that user's home. A Phase 1 `answers.json` SHALL be split into these on first run. MCPJungle SHALL run per user as `mcpjungle@<user>`, on a port recorded in `system.json`, and agent wiring SHALL use that port. `--yes` without `--user` or a profile SHALL re-apply for every user set up.

#### Scenario: Second user
- **WHEN** devbox-setup runs for `ada` and then for `bob` on the same VM, both with `mcpjungle`
- **THEN** two gateways run on different ports, and re-running for `ada` with `--yes --user ada` doesn't change bob's answers or gateway

### Requirement: The home answers file is untrusted
devbox-setup SHALL read and write a user's `~/.config/devbox/answers.json` without following symlinks, SHALL refuse it unless it is a regular file owned by that user or root and at most 1 MiB, and SHALL ignore system items picked in it, with a warning. Ports and the user list SHALL NOT be read from it.

#### Scenario: System item in a user file
- **WHEN** a user adds `ufw` to the picks in their `~/.config/devbox/answers.json` and root runs `devbox-setup --yes`
- **THEN** ufw is not installed, and the output warns that the user file can't pick system items

#### Scenario: Symlinked file
- **WHEN** `~/.config/devbox/answers.json` is a symlink to `/etc/shadow`
- **THEN** devbox-setup refuses to read it and says why, without showing its contents

### Requirement: MCP gateway service user
Each user's gateway and its MCP containers SHALL run as a system user `mcp-<user>`. That user SHALL own `~<user>/.mcp/*/env`, with mode 0600, so the login user cannot read the tokens without sudo. devbox-setup and `mcp-sync` SHALL edit those files through sudo. An opt-in `sudopw` item SHALL remove the login user's `NOPASSWD` sudo rule.

#### Scenario: Agent can't read tokens
- **WHEN** a process running as `ada` without sudo reads `~/.mcp/github/env`
- **THEN** it gets permission denied, and the github MCP server still works through the gateway

### Requirement: Unverified downloads listed
`~/devbox-setup.txt` SHALL list, under "Installed without a pinned checksum", every installed item that has an `unverified` download, with its reason.

#### Scenario: Tailscale installed
- **WHEN** `tailscale` is installed using the vendor script marked `unverified`
- **THEN** `~/devbox-setup.txt` lists Tailscale and its reason
