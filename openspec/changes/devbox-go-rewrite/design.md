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
- **The runner keeps today's semantics.** It runs items in plan order. An item fails on its first failing step. Dependents of a failed item (including mise for mise items) are skipped with "(skipped: X failed)". The run continues and exits 1 listing the failed items. The summary file at `~/devbox-setup.txt` keeps the same three groups. Every command is logged to `/var/log/devbox-setup.log` with its output, and the terminal shows a spinner per item, plus the output tail on failure.
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
- **Credentials are a step type, not a store.**
  - `cred{file, mode, keys[{name, secret, how, required}], apply, logged_in_check}` ports `CRED_ROWS` from `devbox-credentials-flow` unchanged.
  - The UI asks, the step writes, and the runner builds "Secrets to fill in" from the files.
  - Profiles and answers never carry values, and neither does pmox. A profile may name *which* credential items to set up, as picks.
- **Layered profiles.**
  - `--profile` is repeatable, and later files win for scalars (`[git]`, `[system]`, `user`).
  - `picks` and `mise_extra` are unioned.
  - The intended split: a team file with picks and MCP servers, and a personal file with `[git]`, `tz` and `user`.
  - A profile may set `name = "team"`. Answers then record which picks came from which named profile. Re-applying a profile with that name *replaces* its layer, and the review lists what it dropped. Nothing is uninstalled (a non-goal); a dropped item just stops being re-applied.
  - Picks the user made by hand are their own layer and are never dropped by a profile.
- **`user` in profiles.**
  - `user = "ada"`, or `--user ada`, chooses the target account. If it doesn't exist, it's created with the same groups pmox's cloud-init user gets.
  - Without either, the saved user or `SUDO_USER` is used, as today.
- **Version lock.**
  - `devbox-setup export --lock` adds `[lock]` with the resolved mise versions and apt package versions.
  - With a `[lock]`, mise installs exactly those versions, and apt installs them when they're still in the archive (with a warning otherwise).
  - Without a lock, behaviour is unchanged (`@latest`).
- **Local overrides.** These are never written by devbox-setup, and are sourced or included last:
  - `zshrc.tmpl` sources `~/.zshrc.local`
  - `shell.sh` sources `~/.config/devbox/local.sh`
  - `tmux.conf` runs `source-file -q ~/.tmux.local.conf`
  - `vimrc` sources `~/.vimrc.local`

  The `.orig` rule stays as it is, but the README points to the local files as the supported way to keep your own lines.
- **Per-user state.**
  - System items (apt, units, sysctl) stay global in `/var/lib/devbox-setup/system.json`.
  - User items and settings go in `/var/lib/devbox-setup/users/<user>.json`.
  - Phase 1's single `answers.json` is split on first run.
  - MCPJungle becomes a template unit, `mcpjungle@<user>`, with a port allocated from 8080 upwards and recorded in that user's answers. Agent wiring uses that port.
- **The `mcp` service user.**
  - The gateway and its docker containers run as a system user `mcp-<user>`, which owns `~<user>/.mcp/*/env`: 0600, readable only by that service user.
  - devbox-setup and `mcp-sync` edit the files through sudo.
  - This keeps an agent running as the login user from reading every MCP token directly. It doesn't stop a user with passwordless sudo, and the README says so.
  - The opt-in `sudopw` item drops `NOPASSWD` for that user.
- **Supply chain.**
  - Every `apt_repo` needs `key_fingerprint`; the step checks it with `gpg --show-keys` before installing the key.
  - Every download step needs `sha256`, or `unverified = "reason"`.
  - Unverified items (vendor `curl | sh` installers whose scripts change in place) are listed in `~/devbox-setup.txt` under "Installed without a pinned checksum".
  - A test keeps that list explicit.
- **Fleet operations from pmox.**
  - `pmox devbox apply [--all | --tag T | vm...] --profile f.toml` pushes the binary and profile to each VM, runs `sudo devbox-setup --profile … --yes` in parallel (4 at a time), and prints a result line per VM.
  - `pmox devbox verify` does the same with `--verify --profile`, reporting drift per VM.
  - `pmox devbox push` also copies `~/.config/pmox/devbox/catalog.d/*.toml` to `/etc/devbox-setup/catalog.d/` and `~/.config/pmox/devbox/mcp-catalog/*.spec` to the MCP catalog. This is how a team ships its own items and MCP servers without tack.
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
- Should profiles allow `catalog.d`-style extra items (with steps)? Proposed: no in v1, since profiles stay data-only. Teams ship items through `pmox devbox push`'s `catalog.d` copy instead.
- Should the `mcp-<user>` service user be the default, or opt-in? Proposed: default for new installs, with existing VMs migrated on the next run (the env files are re-owned).
