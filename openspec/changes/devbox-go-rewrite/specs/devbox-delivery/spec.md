## MODIFIED Requirements

### Requirement: No size limit from user-data
pmox SHALL install devbox-setup as the static binary for the VM's architecture (from `uname -m`) at `/usr/local/bin/devbox-setup`, with the mise registry snapshot embedded in it. The user-data size SHALL NOT depend on devbox-setup.

#### Scenario: arm64 VM
- **WHEN** devbox-setup is pushed to a VM whose `uname -m` is `aarch64`
- **THEN** `/usr/local/bin/devbox-setup` is the linux/arm64 build, and `devbox-setup --version` prints the pmox version

#### Scenario: Unsupported architecture
- **WHEN** the VM's architecture is neither x86_64 nor aarch64
- **THEN** nothing is copied, and a warning names the architecture

## ADDED Requirements

### Requirement: Team catalog shipped by pmox
`pmox devbox push` (and the launch push) SHALL copy `~/.config/pmox/devbox/catalog.d/*.toml` to `/etc/devbox-setup/catalog.d/`, and `~/.config/pmox/devbox/mcp-catalog/*.spec` to the VM's MCP catalog, when they exist. Files it copied earlier but that are gone locally SHALL be removed. Other files in those directories SHALL be left alone.

#### Scenario: Company MCP server
- **WHEN** `~/.config/pmox/devbox/mcp-catalog/acme-docs.spec` exists and `pmox devbox push web1` runs
- **THEN** the MCPJungle server list on web1 offers `acme-docs`

### Requirement: Apply and verify across VMs
- `pmox devbox apply [--all | --tag <t> | <vm>...] --profile <file>` (repeatable `--profile`) SHALL push to each selected running VM and run `sudo devbox-setup --profile … --yes`, at most 4 at a time. It SHALL print one result line per VM, and exit non-zero if any VM failed.
- `pmox devbox verify` with the same selectors SHALL run `devbox-setup --verify`, with the profiles when given, and report missing items and drift per VM.

#### Scenario: Team rollout
- **WHEN** `pmox devbox apply --tag team --profile team.toml` runs against three VMs and one is unreachable
- **THEN** two VMs report ok, one reports the SSH error, and the exit code is non-zero

#### Scenario: Drift
- **WHEN** one VM has had `starship` removed by hand
- **THEN** `pmox devbox verify --tag team --profile team.toml` reports `starship` missing for that VM only
