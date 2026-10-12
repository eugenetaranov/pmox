## MODIFIED Requirements

### Requirement: No size limit from user-data
pmox SHALL install devbox-setup as the static binary for the VM's architecture (from `uname -m`) at `/usr/local/bin/devbox-setup`, with the mise registry snapshot embedded in it. The user-data size SHALL NOT depend on devbox-setup.

#### Scenario: arm64 VM
- **WHEN** devbox-setup is pushed to a VM whose `uname -m` is `aarch64`
- **THEN** `/usr/local/bin/devbox-setup` is the linux/arm64 build, and `devbox-setup --version` prints the pmox version

#### Scenario: Unsupported architecture
- **WHEN** the VM's architecture is neither x86_64 nor aarch64
- **THEN** nothing is copied, and a warning names the architecture
