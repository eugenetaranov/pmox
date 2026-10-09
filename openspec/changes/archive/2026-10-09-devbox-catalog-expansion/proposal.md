## Why

Five reviewers checked the devbox-setup catalog: a junior developer, a senior developer, a Linux admin, a DevOps/SRE engineer and a platform/Kubernetes engineer. Four of the five hit the same gap: the menu installs tools such as zoxide, fzf, direnv and delta but never turns them on. Git is left on the stock defaults.

The review also found real bugs:
- the bootcmd that turns off auto-upgrades runs on every boot;
- the mise PATH line is never added to an empty `.bashrc`;
- an apt conffile prompt aborts the install;
- the Tailscale auth key is visible in `ps`;
- `sysstat` collects nothing.

The catalog also misses whole areas: databases, IaC linting, Kubernetes beyond the basic CLIs, system tuning, and opt-in hardening. Fixing all of this now, while the tabbed picker is fresh, keeps the menu coherent as it grows past 60 items.

## What Changes

- **Bug fixes**
  - The `stopAutoUpgrades` bootcmd yields to an opt-in marker.
  - The mise PATH line is written correctly into an empty `.bashrc`.
  - apt keeps existing config files instead of prompting (`--force-confdef/confold`).
  - needrestart no longer restarts services mid-run.
  - The Tailscale key is passed by file.
  - Signing keys are downloaded to a temp file first.
  - `sysstat` is enabled.
  - Duplicate and transitional packages are cleaned up.
  - The mcpjungle readiness timeout warns, and a stale comment is fixed.
- **Shell and git setup that turns the tools on**
  - A pmox-owned shell drop-in (zoxide, direnv, fzf bindings, eza/bat aliases, `EDITOR` follows nvim) is sourced from `.zshrc` and `.bashrc`.
  - zsh-autosuggestions and zsh-syntax-highlighting are added.
  - A new "Git defaults" item: rebase on pull, push autoSetupRemote, prune on fetch, rerere, zdiff3, delta, a global ignore file and aliases.
  - A new "tmux config" item.
- **System tuning items, all opt-in**
  - dev limits (inotify, nofile, map count)
  - zram swap
  - journald cap
  - timezone and locale
  - Docker `daemon.json` (log rotation, a non-clashing address pool)
  - etckeeper
  - chrony synced to the host clock (PTP)
- **Security items, all opt-in**
  - SSH keys only
  - ufw
  - fail2ban
  - automatic security updates
- **Catalog**
  - "Cloud & K8s" splits into **Cloud**, **IaC** and **Kubernetes** tabs, and a **Dev tools** tab is added.
  - New items cover:
    - developer CLIs (lazygit, just, watchexec, hyperfine, xh/hurl)
    - linters and hooks
    - database clients
    - container tools
    - IaC lint and scan
    - Kubernetes extras, GitOps and local clusters (k3d, k3s)
    - AI agents (OpenCode, Aider, Goose, Ollama)
    - spec-driven development: OpenSpec (`npm:@fission-ai/openspec`)
    - agent add-ons: ECC (affaan-m/ecc: subagents, skills and hooks for Claude Code) and a box-description `CLAUDE.md`/`AGENTS.md`
    - admin and network tools
    - GitLab CLI and Hetzner CLI
  - Language rows gain their usual companions:
    - **Go:** gopls, delve, govulncheck
    - **Python:** 3.13 + ruff
    - **Rust:** rust-analyzer, cargo-binstall
    - **Java:** 25 Temurin
  - Neovim moves to mise (apt ships 0.9.5).
  - zellij moves to mise.
  - Helm is pinned to 3.
  - Labels say what an item is; slow installs are marked "large".
- **MCP wiring** reaches Codex and Gemini as well as Claude. **BREAKING (saved answers):** `A_WIRE_CLAUDE` becomes `A_WIRE_AGENTS`; it is migrated in `load_answers`.

## Capabilities

### New Capabilities
- `devbox-setup-runtime`: how devbox-setup runs safely and idempotently: apt behavior, key downloads, secrets out of argv, failure warnings, migration of saved answers, and pmox-owned config files kept apart from the synced tack-roles assets.
- `devbox-shell-config`: the shell drop-in and the Git, tmux and editor defaults that activate the installed tools.
- `devbox-system-tuning`: opt-in system settings (limits, swap, journald, time, locale, Docker daemon, etckeeper, sysstat).
- `devbox-security-options`: opt-in hardening (sshd, ufw, fail2ban, unattended security upgrades) and how it interacts with pmox's boot-time auto-upgrade switch-off.
- `devbox-catalog`: the tabs, items, labels and install sources the picker offers, including AI-agent MCP wiring.

### Modified Capabilities
<!-- None: no existing spec covers devbox-setup; the bootcmd behavior is specified under devbox-security-options. -->

## Impact

- **Code:**
  - `internal/bootstrap/devbox-setup` gets most of the changes.
  - `internal/bootstrap/bootstrap.go`: the bootcmd guard, plus a new embedded `conf/` directory.
  - `internal/bootstrap/bootstrap_test.go`
  - `internal/bootstrap/picker.py`: no change expected.
- **Assets:** `internal/bootstrap/assets/` stays a pure tack-roles copy, because `task bootstrap:sync` deletes it. New pmox-owned files go in `internal/bootstrap/conf/`. Template changes that should be shared (zsh plugins) are proposed upstream to tack-roles separately.
- **VM behavior:** only items the user ticks change anything. Existing VMs pick up the fixes on their next `sudo devbox-setup --yes`. The bootcmd guard ships to new VMs only, through cloud-init.
- **Docs:** README and llms.txt sections on devbox-setup, and the `~/devbox-setup.txt` notes.
