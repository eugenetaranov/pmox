## Purpose

The user-facing documentation surface for pmox: `README.md`,
`llms.txt`, `docs/pve-setup.md`, the `examples/` tree, and the
offline link checker (`internal/tools/doccheck`) that keeps internal
references in sync. These artifacts are the contract for how a new
user (or an LLM working in this repo) learns what pmox does, how to
configure it, and which commands exist.

## Requirements

### Requirement: README covers every shipped command

The `README.md` SHALL document every command shipped in slices 1–8
with at least a one-line summary and one example.

#### Scenario: README command reference is complete
- **WHEN** the repository is at v1 and `README.md` is read
- **THEN** the commands section SHALL reference `configure`,
  `create-template`, `launch`, `clone`, `list`, `info`, `start`,
  `stop`, `delete`, `shell`, `exec`, `cp`, `sync`, `mount`,
  `umount`
- **AND** each command SHALL have one summary line and at least
  one invocation example
- **AND** every flag shipped in slices 5–8 (`--cpu`, `--mem`,
  `--disk`, `--storage`, `--snippet-storage`, `--bridge`,
  `--node`, `--wait`, `--no-wait-ssh`, `--post-create`, `--tack`,
  `--ansible`, `--strict-hooks`, `--yes`, `--force`,
  `--regen-cloud-init`) SHALL appear at least once in the README

#### Scenario: README leads with a working quick start
- **WHEN** a reader follows the quick-start section
- **THEN** the sequence SHALL be: install, configure, launch,
  shell, delete
- **AND** each step SHALL be copy-pasteable without placeholders
  other than hostnames the reader owns

### Requirement: README cloud-init example includes SSH keys

The README cloud-init section SHALL lead with a complete working
snippet that includes an `ssh_authorized_keys:` block.

#### Scenario: Cloud-init example is copy-pasteable
- **WHEN** a reader copies the first cloud-init snippet from the
  README
- **THEN** the snippet SHALL contain `ssh_authorized_keys:` under a
  `users:` entry
- **AND** the snippet SHALL enable and start `qemu-guest-agent`
- **AND** the snippet SHALL NOT contain placeholders other than the
  SSH key value

### Requirement: README exit code table

The README SHALL include a table of every exit code defined in
`internal/exitcode`.

#### Scenario: Exit code table is complete
- **WHEN** the README is rendered
- **THEN** the exit code table SHALL contain one row per named
  constant in `internal/exitcode`: `ExitOK` (0), `ExitGeneric` (1),
  `ExitUserError` (2), `ExitNotFound` (3), `ExitAPIError` (4),
  `ExitNetworkError` (5), `ExitUnauthorized` (6), `ExitTimeout` (7),
  `ExitHook` (8)
- **AND** each row SHALL have columns `Code`, `Name`, `Meaning`

### Requirement: llms.txt at repo root

The repository SHALL ship `llms.txt` at the root following the
llmstxt.org convention as a flat markdown file no larger than 15 KB.

#### Scenario: llms.txt size and shape
- **WHEN** the file is read at v1
- **THEN** its total size SHALL be at most 15 360 bytes
- **AND** it SHALL contain sections `# pmox`, `## Commands`,
  `## Flags`, `## Exit codes`, `## Config file`, `## Examples`,
  `## Links`

#### Scenario: llms.txt enumerates every command
- **WHEN** the Commands section is parsed
- **THEN** every command shipped in slices 1–8 SHALL appear with a
  one-line summary

### Requirement: Examples directory

The repository SHALL ship an `examples/` directory with runnable
reference files for cloud-init, post-create scripts, tack configs,
and Ansible playbooks.

#### Scenario: Examples tree is present and indexed
- **WHEN** the repository is at v1
- **THEN** `examples/cloud-init.yaml`,
  `examples/post-create.sh`, `examples/tack.yaml`,
  `examples/ansible/playbook.yaml`, and `examples/README.md` SHALL
  all exist
- **AND** `examples/README.md` SHALL contain a one-paragraph
  description of each file plus the `pmox launch` invocation that
  exercises it
- **AND** `examples/post-create.sh` SHALL be executable (mode 0755)

#### Scenario: Examples are referenced from README and llms.txt
- **WHEN** the docs-check target runs
- **THEN** every file under `examples/` that is intended for end
  users SHALL be reachable via a relative link from `README.md`,
  `llms.txt`, or `examples/README.md`

### Requirement: PVE-side setup doc

The repository SHALL ship `docs/pve-setup.md` covering API token
creation, required permissions, node SSH access, template
preparation, and common first-launch errors.

#### Scenario: PVE setup doc exists and is linked
- **WHEN** the repository is at v1
- **THEN** `docs/pve-setup.md` SHALL exist
- **AND** `README.md` SHALL link to it from its PVE-side-setup
  section

### Requirement: Link checker

The `Makefile` SHALL expose a `docs-check` target that validates
every relative link in `README.md`, `llms.txt`, `docs/*.md`, and
`examples/README.md` resolves to an existing file in the
repository.

#### Scenario: docs-check prefers lychee when available
- **WHEN** `make docs-check` runs and `lychee` is on PATH
- **THEN** it SHALL invoke `lychee --offline README.md llms.txt
  docs/ examples/`
- **AND** otherwise it SHALL fall back to
  `go run ./internal/tools/doccheck`

#### Scenario: Broken internal link fails the check
- **WHEN** `make docs-check` runs against a tree where `README.md`
  references `./examples/nonexistent.yaml`
- **THEN** the command SHALL exit non-zero
- **AND** SHALL name the broken link with its source file and line
  number

#### Scenario: Healthy tree passes
- **WHEN** `make docs-check` runs against the v1 tree
- **THEN** the command SHALL exit 0

#### Scenario: External links are not fetched
- **WHEN** a doc file contains `https://`, `http://`, or `mailto:`
  links
- **THEN** the link checker SHALL NOT attempt to fetch them
- **AND** SHALL NOT fail when they are unreachable

#### Scenario: CI runs docs-check on doc changes
- **WHEN** a PR modifies any file under `README.md`, `llms.txt`,
  `docs/`, `examples/`, `internal/tools/doccheck/`, or the
  `Makefile`
- **THEN** CI SHALL run `make docs-check`
- **AND** the PR SHALL be blocked if the check fails
