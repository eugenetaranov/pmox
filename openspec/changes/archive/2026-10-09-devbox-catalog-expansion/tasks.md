## 1. Foundations and bug fixes

- [x] 1.1 Add `internal/bootstrap/conf/`, embed it in `bootstrap.go`, and ship it to `$SHARE/conf/`. Add a test that the files appear in `write_files`.
- [x] 1.2 Guard the `stopAutoUpgrades` bootcmd with `[ -e /etc/devbox-setup/auto-upgrades ] ||`. Update the starter cloud-init template and `bootstrap_test.go`.
- [x] 1.3 Add `--force-confdef --force-confold` to `APT_OPTS` and to `90devbox-setup`. Export `NEEDRESTART_MODE=l` in `apt_install`.
- [x] 1.4 Fix the mise PATH line for an empty or missing `.bashrc` in `install_mise`.
- [x] 1.5 Pass the Tailscale auth key through a 0600 temporary file (`--auth-key=file:`).
- [x] 1.6 Make `add_key` download to a temporary file, check it is non-empty, then move it into place.
- [x] 1.7 Warn when the mcpjungle readiness loop times out. Fix the stale `--host=` comment.
- [x] 1.8 Add the `load_answers` migrations: `editors` → `editors neovim`, and `A_WIRE_CLAUDE` → `A_WIRE_AGENTS`.

## 2. Shell and Git setup

- [x] 2.1 Write `conf/shell.sh`: zoxide, direnv and fzf (zsh and bash), eza/bat aliases and `EDITOR`. Every hook is guarded by `command -v`.
- [x] 2.2 Add the `shellcfg` row and `install_shellcfg`: copy the drop-in and append the source line once to `.zshrc` and `.bashrc`. Make `install_shell` re-append it when `shellcfg` is planned.
- [x] 2.3 Clone zsh-autosuggestions and zsh-syntax-highlighting in `install_shell`. Extend `ZSH_PLUGINS`, with syntax-highlighting last.
- [ ] 2.6 Open a tack-roles PR for the same plugins (follow-up, outside this repo).
- [x] 2.4 Add the `gitcfg` row and `install_gitcfg`: the settings, aliases and delta (when present), plus `conf/gitignore_global` written only when absent.
- [x] 2.5 Add `conf/tmux.conf` and the `tmuxcfg` row, keeping an existing file as `.orig`.

## 3. System tuning

- [x] 3.1 Add `etckeeper` as the first System row.
- [x] 3.2 Add `devlimits` (sysctl, limits.d and system.conf.d drop-ins, then `sysctl --system`).
- [x] 3.3 Add `zram` (systemd-zram-generator config and swappiness).
- [x] 3.4 Add `journald` (drop-in, restart).
- [x] 3.5 Add the `tzlocale` ask: validate the timezone, generate the locale, install `locales-all`, and save `A_TZ` in `ANSWER_VARS`.
- [x] 3.6 Add `conf/daemon.json` and write it in `install_docker` only when absent, before `systemctl enable --now docker`.
- [x] 3.7 Add `chrony` with ptp_kvm and the PHC refclock, falling back to NTP pools when `/dev/ptp0` is missing.
- [x] 3.8 Clean up the `debug` list: bind9-dnsutils, iotop-c and the new network tools, with sysstat enabled. Remove `htop` from `cli`, and `build-essential` from `cpp`.

## 4. Security options

- [x] 4.1 Add `autoupdates`: unattended-upgrades, `20auto-upgrades`, `52devbox`, the marker and the timers. Warn when the user-data has the unguarded bootcmd.
- [x] 4.2 Add `sshharden`: an `authorized_keys` guard, `10-devbox.conf`, `sshd -t`, and a reload or rollback.
- [x] 4.3 Add `ufw`, with rules depending on the plan (tailscale0, mosh, mcpjungle) and enable last.
- [x] 4.4 Add `fail2ban` (`jail.d/devbox.local`, systemd backend, Tailscale CGNAT ignored).
- [x] 4.5 Add `needrestart` (quiet auto-restart for the user's own apt runs).

## 5. Catalog and tabs

- [x] 5.1 Update `SCREENS` to the nine tabs. Move the existing rows to Cloud, IaC and Kubernetes.
- [x] 5.2 Split `editors` into apt `vim` and a new mise `neovim` row. Make `zellij` a mise row and drop `install_zellij`.
- [x] 5.3 Update the language rows: the Go `go:` tools, `python@3.13` + ruff, Rust `cargo-binstall` + the rust-analyzer component, `java@temurin-25`, and `helm@3` in `k8s`.
- [x] 5.4 Add the Dev tools rows: lazygit, runners, http, lint, dbclients, pgcli, profiling, act.
- [x] 5.5 Add the System and Shell rows: ctools, netfs, monitoring, mosh, docs.
- [x] 5.6 Add the Cloud and IaC rows: hcloud, cloudflared, backup, iaclint, iacscan, vault.
- [x] 5.7 Add the Kubernetes rows: k8sextra, krew (fn), helmfile + helm-diff (fn), gitops, k3d, k3s (fn), devloop, talos.
- [x] 5.8 Add `glab` to Accounts.
- [x] 5.9 Apply the label changes from the spec, including the "(large)" markers.

## 6. AI tab

- [x] 6.1 Add opencode, aider and goose rows, and an ollama fn (listening on 127.0.0.1, labelled large/CPU-only).
- [x] 6.2 Add the `openspec` row (`npm:@fission-ai/openspec@latest`, needs node).
- [x] 6.3 Choose and pin an ECC version. Verify that `install --profile minimal --target claude` runs headless, then add the `ecc` fn row (needs node and claude).
- [x] 6.4 Add `agentcfg`: `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md` from `conf/`, written only when absent.
- [x] 6.5 Rename the wiring prompt to cover all agents. Register mcpjungle with Claude, Codex and Gemini when they are picked, skipping any that already have it.

## 7. Verification and docs

- [x] 7.1 Check the uncertain mise names (`hurl`, `usql`, `aqua:block/goose`, `go:` backend ordering, `krew`, `helm@3` with helm-diff) on a noble VM. Fix the specs or drop rows that fail.
- [x] 7.2 Run `devbox-setup --yes` with every id picked on a fresh noble VM. Record the failures and fix them.
- [x] 7.3 Re-run on the same VM to confirm idempotence: no duplicated rc lines, git config or ufw rules.
- [x] 7.4 Check that old answers migrate: an answers file with `editors` and `A_WIRE_CLAUDE=yes` runs with `--yes`.
- [x] 7.5 Update the README and llms.txt devbox-setup sections, and the `write_manage_notes` wording for the new config items.
- [x] 7.6 Run `shellcheck internal/bootstrap/devbox-setup` and `go test ./internal/bootstrap/...`.
