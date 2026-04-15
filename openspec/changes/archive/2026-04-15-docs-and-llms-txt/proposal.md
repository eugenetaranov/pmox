## Why

Slices 1–8 ship every feature pmox v1 needs, but the README is
still a one-line placeholder from slice 1 and there's no `llms.txt`
or `examples/` tree. Users arriving at `github.com/eugenetaranov/pmox`
today see a binary they can install via `brew install` with zero
usage guidance. LLM-driven development workflows (Claude Code,
Cursor, Cody) rely on `llms.txt` for a canonical, grep-friendly
summary; tack already ships one and pmox should mirror that
pattern.

This is the final pre-v1 slice: pure documentation. No code
changes, no new commands, no new dependencies.

## What Changes

- Replace `README.md` with a full user-facing guide. Sections:
  one-paragraph pitch, install (brew tap + `go install`),
  PVE-side setup (API token scopes, template requirements —
  `qemu-guest-agent` installed and `agent: 1` per D-T3), quick
  start (configure + launch one VM), command reference with
  one-line descriptions and one example per command, cloud-init
  section leading with a complete working snippet that includes
  SSH-key injection (per D-T5 operational consequence), hooks
  section explaining `--post-create` / `--tack` / `--ansible` /
  `--strict-hooks`, config file schema, exit code table, troubleshooting.
- Create `llms.txt` at the repo root following the tack
  convention: project name + one-paragraph description, a
  `## Commands` section listing every command and its flags, a
  `## Exit codes` table, a `## Config file` block, a `## Links`
  section pointing to the README and to the tack companion
  project. Flat markdown, no HTML, no tables that wrap awkwardly.
- Create `examples/` containing: `examples/cloud-init.yaml` (moved
  from slice 7 if it lives at repo root, or kept if already under
  `examples/`), `examples/post-create.sh` (a minimal script that
  reads `$PMOX_IP` and runs `cloud-init status --wait` over SSH),
  `examples/tack.yaml` (minimal tack config that installs
  `htop`), `examples/ansible/playbook.yaml` (minimal Ansible
  playbook that installs `htop`), `examples/README.md` indexing
  the four.
- Link `examples/*` from both `README.md` and `llms.txt`.
- Add a `docs/` directory with one page: `docs/pve-setup.md`
  covering API token creation, role assignment (`PVEVMAdmin` +
  `Datastore.AllocateSpace`), template preparation, and
  troubleshooting the most common first-launch errors. The
  README links to it rather than inlining everything.
- Smoke-test the docs: add a `make docs-check` target that runs
  a link checker (`lychee` if available, else a stdlib Go
  round-trip that resolves every local `./examples/*` and
  `./docs/*` reference). CI runs this target on PRs touching
  `README.md`, `llms.txt`, or `docs/`.

## Capabilities

### New Capabilities
- `docs-and-llms-txt`: the documentation surface itself —
  README, llms.txt, docs/, examples/, and the link-check tooling.

### Modified Capabilities
- None. No code is touched; the existing command behaviors are
  documented as-is.

## Impact

- **New files**: `llms.txt`, `docs/pve-setup.md`, `examples/README.md`,
  `examples/post-create.sh`, `examples/tack.yaml`,
  `examples/ansible/playbook.yaml`. Possibly `examples/cloud-init.yaml`
  if slice 7 put it elsewhere.
- **Modified files**: `README.md` (complete rewrite), `Makefile`
  (new `docs-check` target), `.github/workflows/ci.yaml` (new
  job that runs `make docs-check` when doc paths are touched).
- **New dependencies**: none runtime; optional `lychee` as a
  build-time dev tool.
- **Cross-slice contract**: the command reference in README and
  llms.txt both list the exact flag set shipped by slices 5–8.
  If a flag is added later, both files update as part of that
  slice.
