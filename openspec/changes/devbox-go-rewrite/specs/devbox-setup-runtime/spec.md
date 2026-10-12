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
System items' answers SHALL be kept in `/var/lib/devbox-setup/system.json`, and each user's items and settings in `/var/lib/devbox-setup/users/<user>.json`. A Phase 1 `answers.json` SHALL be split into these on first run. MCPJungle SHALL run per user as `mcpjungle@<user>`, on a port recorded in that user's answers, and agent wiring SHALL use that port.

#### Scenario: Second user
- **WHEN** devbox-setup runs for `ada` and then for `bob` on the same VM, both with `mcpjungle`
- **THEN** two gateways run on different ports, and re-running for `ada` with `--yes` doesn't change bob's answers or gateway

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
