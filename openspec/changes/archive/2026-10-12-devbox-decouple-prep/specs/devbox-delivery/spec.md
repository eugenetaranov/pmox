## ADDED Requirements

### Requirement: devbox-setup copied over SSH at launch
When devbox-setup is enabled for the server, `pmox launch` SHALL install devbox-setup's files after the SSH and boot waits and before any hook, by streaming one gzip tar over SSH and extracting it as root at `/`. pmox SHALL NOT add devbox-setup's files to the VM's user-data. A failed copy SHALL print a warning naming the VM and `pmox devbox push <vm>`, and SHALL NOT fail the launch or remove the ready tag.

#### Scenario: Normal launch
- **WHEN** `pmox launch web1` runs with devbox-setup enabled
- **THEN** the uploaded user-data has no `/usr/local/bin/devbox-setup` entry, and after launch `/usr/local/bin/devbox-setup` and `/usr/local/share/devbox-setup/` exist on the VM

#### Scenario: Hook can use it
- **WHEN** `pmox launch web1 --post-create ./setup.sh` runs and `setup.sh` calls `sudo devbox-setup --yes`
- **THEN** devbox-setup is already installed when the hook starts

#### Scenario: Copy fails
- **WHEN** the SSH copy fails
- **THEN** launch exits 0 with the VM marked ready, and stderr has a warning that includes `pmox devbox push web1`

#### Scenario: No SSH wait
- **WHEN** `pmox launch web1 --no-wait-ssh` runs
- **THEN** no copy is attempted, and stderr says devbox-setup was not installed and how to push it

### Requirement: `pmox devbox push`
`pmox devbox push <vm>` SHALL install or refresh devbox-setup's files on a running VM using the same tarball as launch, and SHALL leave `/var/lib/devbox-setup` untouched. It SHALL resolve the VM like `pmox shell` does, including the interactive picker when no VM is given on a terminal.

#### Scenario: Refresh keeps answers
- **WHEN** a VM has saved answers and `pmox devbox push web1` runs
- **THEN** the script and share dir match this pmox version, and `/var/lib/devbox-setup/answers.json` is unchanged

### Requirement: No size limit from user-data
The files pmox installs SHALL include the full mise registry snapshot (names, aliases and descriptions), and the user-data size SHALL no longer depend on devbox-setup.

#### Scenario: Full registry on the VM
- **WHEN** devbox-setup is pushed to a VM
- **THEN** `/usr/local/share/devbox-setup/mise-registry.tsv` equals `internal/bootstrap/mise-registry.tsv`
