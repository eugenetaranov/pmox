## ADDED Requirements

### Requirement: README covers every shipped command

The `README.md` SHALL document every command shipped in slices 1–8 with at least a one-line summary and one example.

#### Scenario: README command reference is complete
- **WHEN** the repository is at v1 and `README.md` is read
- **THEN** the commands section SHALL reference `configure`, `launch`, `list`, `info`, `start`, `stop`, `delete`, `clone`
- **AND** each command SHALL have one summary line and at least one invocation example
- **AND** every flag listed in the slice-5–8 spec files SHALL appear at least once in the README

#### Scenario: README leads with a working quick start
- **WHEN** a reader follows the quick-start section
- **THEN** the sequence SHALL be: install, configure, launch, SSH, delete
- **AND** each step SHALL be copy-pasteable without placeholders other than hostnames the reader owns

### Requirement: README cloud-init example includes SSH keys

The README cloud-init section SHALL lead with a complete working snippet that includes an `ssh_authorized_keys:` block.

#### Scenario: Cloud-init example is copy-pasteable
- **WHEN** a reader copies the first cloud-init snippet from the README
- **THEN** the snippet SHALL contain `ssh_authorized_keys:` under a `users:` entry
- **AND** the snippet SHALL enable and start `qemu-guest-agent`
- **AND** the snippet SHALL NOT contain placeholders other than the SSH key value

### Requirement: README exit code table

The README SHALL include a table of every exit code defined in `internal/exitcode`.

#### Scenario: Exit code table is complete
- **WHEN** the README is rendered
- **THEN** the exit code table SHALL contain one row per named constant in `internal/exitcode` (0 ok, 2 config, 3 auth, 4 remote, 5 timeout, 6 generic, 7 hook)
- **AND** each row SHALL have columns `Code`, `Name`, `Meaning`

### Requirement: llms.txt at repo root

The repository SHALL ship `llms.txt` at the root following the llmstxt.org convention as a flat markdown file no larger than 15 KB.

#### Scenario: llms.txt size and shape
- **WHEN** the file is read at v1
- **THEN** its total size SHALL be at most 15 360 bytes
- **AND** it SHALL contain sections `# pmox`, `## Commands`, `## Flags`, `## Exit codes`, `## Config file`, `## Examples`, `## Links`

#### Scenario: llms.txt enumerates every command
- **WHEN** the Commands section is parsed
- **THEN** every command shipped in slices 1–8 SHALL appear with a one-line summary

### Requirement: Examples directory

The repository SHALL ship an `examples/` directory with runnable reference files for cloud-init, post-create scripts, tack configs, and Ansible playbooks.

#### Scenario: Examples tree is present and indexed
- **WHEN** the repository is at v1
- **THEN** `examples/cloud-init.yaml`, `examples/post-create.sh`, `examples/tack.yaml`, `examples/ansible/playbook.yaml`, and `examples/README.md` SHALL all exist
- **AND** `examples/README.md` SHALL contain a one-paragraph description of each file
- **AND** `examples/post-create.sh` SHALL be executable (mode 0755)

#### Scenario: Examples are referenced from README and llms.txt
- **WHEN** the docs-check target runs
- **THEN** every file under `examples/` SHALL be reachable via a relative link from `README.md` or `llms.txt` or `examples/README.md`

### Requirement: PVE-side setup doc

The repository SHALL ship `docs/pve-setup.md` covering API token creation, required permissions, template preparation, and common first-launch errors.

#### Scenario: PVE setup doc exists and is linked
- **WHEN** the repository is at v1
- **THEN** `docs/pve-setup.md` SHALL exist
- **AND** `README.md` SHALL link to it from its PVE-side-setup section

### Requirement: Link checker

The `Makefile` SHALL expose a `docs-check` target that validates every relative link in `README.md`, `llms.txt`, `docs/*.md`, and `examples/README.md` resolves to an existing file in the repository.

#### Scenario: Broken internal link fails the check
- **WHEN** `make docs-check` runs against a tree where `README.md` references `./examples/nonexistent.yaml`
- **THEN** the command SHALL exit non-zero
- **AND** SHALL name the broken link

#### Scenario: Healthy tree passes
- **WHEN** `make docs-check` runs against the v1 tree
- **THEN** the command SHALL exit 0

#### Scenario: CI runs docs-check on doc changes
- **WHEN** a PR modifies any file under `README.md`, `llms.txt`, `docs/`, or `examples/`
- **THEN** CI SHALL run `make docs-check`
- **AND** the PR SHALL be blocked if the check fails
