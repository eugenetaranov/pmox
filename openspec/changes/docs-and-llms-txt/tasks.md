## 1. README rewrite

- [ ] 1.1 Delete the placeholder `README.md` content
- [ ] 1.2 Write new `README.md` following the section order in design D1
- [ ] 1.3 Pitch paragraph: one sentence on what, one sentence on why (per design D7)
- [ ] 1.4 Install section: Homebrew tap (`brew install eugenetaranov/tap/pmox`), `go install`, raw binary download from releases
- [ ] 1.5 PVE-side setup: 5-line summary linking to `docs/pve-setup.md`
- [ ] 1.6 Quick start: `pmox configure`, `pmox launch web1`, `ssh pmox@<ip>`, `pmox delete web1`
- [ ] 1.7 Commands table: one row per command with `Command | Summary | Example`
- [ ] 1.8 Cloud-init section: complete working snippet with `ssh_authorized_keys`, `qemu-guest-agent`, `runcmd`
- [ ] 1.9 Hooks section: `--post-create` example, `--tack`, `--ansible`, `--strict-hooks`
- [ ] 1.10 Configuration section: `$XDG_CONFIG_HOME/pmox/config.yaml`, keychain note
- [ ] 1.11 Environment variables: list each `PMOX_*` with precedence note
- [ ] 1.12 Exit code table with every constant from `internal/exitcode`
- [ ] 1.13 Troubleshooting: 5–8 common errors with diagnoses
- [ ] 1.14 Development: `make build test lint release-dry-run`
- [ ] 1.15 License: one-line MIT reference

## 2. llms.txt

- [ ] 2.1 Create `llms.txt` at repo root with the exact section structure from design D2
- [ ] 2.2 Pitch block: `> ...` blockquote matching the README pitch
- [ ] 2.3 `## Commands`: bullet list with one line per command
- [ ] 2.4 `## Flags`: persistent root flags plus command-specific flag groups
- [ ] 2.5 `## Exit codes`: markdown table matching README
- [ ] 2.6 `## Config file`: path, schema summary, keychain note
- [ ] 2.7 `## Examples`: bullet list of every file under `examples/`
- [ ] 2.8 `## Links`: README URL, tack companion project URL
- [ ] 2.9 Assert file size ≤ 15 360 bytes (`wc -c llms.txt`); trim if over

## 3. examples/

- [ ] 3.1 Confirm `examples/cloud-init.yaml` exists from slice 7; if not, create it here matching slice 7's spec
- [ ] 3.2 Create `examples/post-create.sh` per design D3, mode 0755
- [ ] 3.3 Create `examples/tack.yaml` — minimal config installing `htop` via tack's apt module (reference `github.com/tackhq/tack/examples/`)
- [ ] 3.4 Create `examples/ansible/playbook.yaml` — minimal playbook installing `htop` via `ansible.builtin.apt`
- [ ] 3.5 Create `examples/README.md` — one paragraph per file, with the `pmox launch` invocation that exercises it
- [ ] 3.6 Link each example from the main README

## 4. docs/pve-setup.md

- [ ] 4.1 Create `docs/pve-setup.md`
- [ ] 4.2 Section 1: API token creation (`pveum user token add` with the required scopes)
- [ ] 4.3 Section 2: Required roles (`PVEVMAdmin`, `Datastore.AllocateSpace`)
- [ ] 4.4 Section 3: Template preparation (`apt install qemu-guest-agent`, `agent: 1`, cloud-init drive)
- [ ] 4.5 Section 4: Common first-launch errors — 403, agent-not-responding, snippets-missing
- [ ] 4.6 Link back to the README cloud-init and troubleshooting sections

## 5. Link checker tool

- [ ] 5.1 Create `internal/tools/doccheck/main.go`
- [ ] 5.2 `main` walks `README.md`, `llms.txt`, `docs/*.md`, `examples/README.md`
- [ ] 5.3 For each file, parse with a regex that matches `[text](./path)` and `[text](path)` where path does not start with `http`
- [ ] 5.4 For each matched path, `os.Stat` relative to the containing file; print path + "not found" on miss
- [ ] 5.5 Exit 0 on clean walk, exit 1 on any miss
- [ ] 5.6 Unit test `doccheck_test.go` with a temp dir tree containing one valid and one broken link; assert exit status and stderr

## 6. Makefile + CI

- [ ] 6.1 Add `docs-check:` target to `Makefile` per design D5
- [ ] 6.2 The target prefers `lychee --offline` if on PATH, falls back to `go run ./internal/tools/doccheck`
- [ ] 6.3 Add a `docs` job to `.github/workflows/ci.yaml` that runs `make docs-check` on pull requests touching `README.md`, `llms.txt`, `docs/**`, or `examples/**`
- [ ] 6.4 Use `dorny/paths-filter@v3` (or equivalent) to trigger the job only on doc changes
- [ ] 6.5 The job runs on `ubuntu-latest`, no caching needed (fast target)

## 7. Verification

- [ ] 7.1 `make docs-check` passes locally
- [ ] 7.2 `wc -c llms.txt` is ≤ 15360
- [ ] 7.3 `pmox --help` output matches the Commands section of README and llms.txt (manual diff)
- [ ] 7.4 Every flag in slices 5–8 spec files appears at least once in README — grep sanity check
- [ ] 7.5 `README.md` renders correctly on GitHub (manual — push to a preview branch)
- [ ] 7.6 `examples/post-create.sh` runs successfully against a real launched VM (manual smoke)
- [ ] 7.7 `examples/tack.yaml` applies successfully via `pmox launch --tack ./examples/tack.yaml` against a real VM
- [ ] 7.8 `examples/ansible/playbook.yaml` applies successfully via `pmox launch --ansible ./examples/ansible/playbook.yaml`
- [ ] 7.9 `go build ./...` and `make lint` still pass (doc changes shouldn't break either, but check)
