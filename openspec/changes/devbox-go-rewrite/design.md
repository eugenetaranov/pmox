## Context

Today's 97 catalog items: 50 install with mise, 11 with apt, 2 are prompts and 34 are bash functions (33 once chrony moves to pmox in Phase 1). The functions use a small set of patterns: add an apt repo and key, write a config file with a `.orig` backup, clone a repo, render a template, enable a unit, set the login shell. About a dozen carry real logic: docker (group, daemon.json, compose), tailscale (key file, `up`), mcpjungle (containers, readiness wait, `mcp-sync`, agent wiring), github (gh login, ssh key upload), gitcfg (defaults from prompts), autoupdates (bootcmd detection), sshharden/ufw (don't lock the user out), k3s, ollama, claude/omp (vendor installers), monitoring.

Existing devbox-* specs describe behaviour, not bash, and they are the acceptance criteria for the port.

## Goals / Non-Goals

**Goals:**
- one static binary with no runtime dependencies beyond what Ubuntu ships
- item behaviour identical to the bash version
- a catalog that most changes edit without writing Go
- profiles
- a structure that moves to its own repo without rewrites

**Non-Goals:**
- distros other than Ubuntu/Debian (kept possible, not done)
- uninstall
- its own release channel or repo (a follow-up once this settles)

## Decisions

- **Layout: inside the pmox module for now.**
  - `cmd/devbox-setup/main.go`, plus packages under `internal/devbox/`:
    - `catalog`: TOML and validation
    - `plan`: resolve and order
    - `steps`: step types and runner
    - `answers`: answers.json and migrations
    - `profile`: profiles
    - `ui`: picker and prompts
  - Being in the same module lets it import `internal/tui` and share CI. Nothing under `internal/devbox` imports pmox's PVE code, and a test checks this, so a later move to its own repo is a `git mv` plus a module path change.
  - *Alternative:* a separate Go module or repo now. Rejected: it would need `internal/tui` published as a module first, plus two release pipelines before the code is stable.
- **Catalog as TOML, embedded** (`go:embed catalog/*.toml`, one file per tab). Validation runs in tests and at start-up: unique ids, needs that exist, no cycles, known step types, files that exist in the embed. Order within a tab is file order, so the review and install order stay as today. *Alternative:* Go structs. Rejected: TOML is diffable, profile-like, and editable by non-Go contributors and in `catalog.d`.
- **Steps are an interface:** `Plan(ctx) []Action` (for the dry run), `Check(ctx) (done bool)` (for verify and idempotence), and `Apply(ctx)`.
  - Declarative types: `apt`, `apt_repo{key_url, key_sha256?, source}`, `mise`, `file{src,to,mode,owner,keep_orig}`, `template{src,to,vars}`, `git_clone{repo,to,ref}`, `link`, `systemctl`, `sysctl`, `user_shell`, `run{cmd, as_user, creates}`.
  - Logic types: `go:<name>` names a registered Go step, such as `go:docker`.
  - `run` exists for one-liners but needs `creates` (or `check`) so re-runs are no-ops.
- **One apt transaction per run.** The runner collects every `apt` and `apt_repo` across the plan, adds repos, then runs one `apt-get update` and one install. It keeps the Phase 0 behaviour: dpkg conf options, `NEEDRESTART_MODE=l`, and the lock retry. A failed install is retried per package so the failure lands on the right item. The same goes for `mise use -g` per user.
- **The runner keeps today's semantics.** It runs items in plan order. An item fails on its first failing step. Dependents of a failed item (including mise for mise items) are skipped with "(skipped: X failed)". The run continues and exits 1 listing the failed items. The summary file at `~/devbox-setup-summary.txt` keeps the same three groups. Every command is logged to `/var/log/devbox-setup.log` with its output, and the terminal shows a spinner per item, plus the output tail on failure.
- **Run as the target user** through a single `asUser(cmd)` helper (`runuser -u`, login env, mise shims on PATH). This replaces the bash `as_user` / `as_user_sh` split.
- **Answers:** read `answers.json` v1 from Phase 1 unchanged, with the same migration table. Secrets (TS auth key, MCP keys) are never saved, as today.
- **Profiles:**
  ```toml
  # devbox profile v1
  picks      = ["essentials", "cli", "zsh", "zshcfg", "p10k", "docker", "go", "k8s"]
  mise_extra = ["starship", "terraform@1.9"]
  [git]
  name  = "Ada Lovelace"
  email = "ada@example.com"
  [system]
  tz = "Europe/Istanbul"
  [mcp]
  servers = ["github", "context7"]
  ```
  - Picks must be catalog ids. Unknown ids are dropped with a warning, and the run doesn't fail, so old profiles keep working.
  - There are no commands, step overrides or secrets. A file with an unknown key is rejected, not ignored, so a typo doesn't quietly do nothing.
  - `--profile` without `--yes` opens the review screen pre-ticked. With `--yes` it applies directly.
  - URLs: https only. The profile is shown and needs confirmation unless `--yes` is given.
  - `export` writes the saved answers (the accumulated picks) as a profile.
- **Profiles from pmox:** `pmox launch --devbox-profile f.toml` (and a `devbox_profile` config default) uploads the profile with the binary and runs `sudo devbox-setup --profile … --yes` after the push. Output streams like a hook. A failure is a warning unless `--strict-hooks` is set.
- **UI:**
  - It reuses `internal/tui` for prompts, spinners and colours.
  - A new multi-tab picker ports `picker.py`'s behaviour: number keys and arrows, type to search, Esc, and Enter on search goes to Review.
  - Scoring rules move as-is. `picker_test.py` cases become Go tests, so ranking can't drift.
  - The mise registry TSV is embedded and refreshed from GitHub with a 7-day cache, as today.
- **Delivery:** pmox embeds the two gzip'd devbox-setup binaries (about 4–5 MB each), built by goreleaser in a `before` step and by `task build` for dev builds.
  - The push detects `uname -m` and copies the matching one to `/usr/local/bin/devbox-setup`.
  - Assets (conf, MCP catalog, mcp-sync) are embedded in the binary, so there is no share dir.
  - *Alternative:* the VM downloads the release asset. Rejected for now: it needs network and checksum plumbing tied to pmox versions, and doesn't work for dev builds.
- **Staged cut-over** behind one switch, so it ships per tab:
  1. The Go binary owns the whole UI and plan from the first slice.
  2. Items not yet ported run through a `legacy` step that calls the bundled bash with `--only <id>`, so the bash stays as an installer library.
  3. Each slice ports one tab's items and deletes their bash.
  4. The last slice removes the script, `picker.py`, gum and the `legacy` step.

## Risks / Trade-offs

- [Behaviour drift while porting about 33 function items] → each port's PR includes a checklist from the item's spec and bash: files, modes, backups, groups, symlinks, idempotence. Verify `Check()` on a re-run.
- [Install behaviour can only really be tested on a VM] → `task devbox:e2e` launches a throwaway Ubuntu VM with pmox, applies a profile with every item, runs `--verify` and re-runs (expecting no changes), then deletes the VM. It runs in CI on manual dispatch and before each release. Unit tests cover the catalog, plan, steps (against a fake exec) and the UI model.
- [pmox gets ~10 MB bigger] → acceptable for a CLI; revisit when devbox-setup gets its own release.
- [Losing quick hand-edits on a VM] → `catalog.d` overrides and `--dry-run` showing exact actions; the log file.
- [About 3–5 weeks of work] → slices deliver value early; the legacy step keeps everything working in between.

## Migration Plan

1. Ship slice 1 (Go UI, plan, answers, profiles, apt/mise/file steps, legacy for the rest) behind `pmox launch` with no flag change. VMs with old answers keep working.
2. Port tab by tab: Shell, Dev, Languages, Cloud/IaC/K8s, AI, System/Accounts.
3. Remove bash. Rollback at any slice: the previous pmox release pushes the previous binary or script.

## Open Questions

- New name? The binary stays `devbox-setup` until the repo split; renaming then is cheap.
- Should profiles allow `catalog.d`-style extra items (with steps)? Proposed: no in v1, since profiles stay data-only.
