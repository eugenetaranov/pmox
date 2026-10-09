## Context

`internal/bootstrap/devbox-setup` is a bash installer that pmox embeds in every VM through cloud-init `write_files`.

**The catalog:**
- Picks come from `CATALOG_ROWS`, with fields `id|screen|label|how|what|needs|source`.
- They are chosen in `picker.py`, a curses tabbed checklist.
- They are installed one row at a time behind a spinner.
- An item is installed with apt, with mise (`mise use -g`), or with a custom `install_<id>` function.
- Choices accumulate in `/var/lib/devbox-setup/answers`; `--yes` re-applies them.

**The constraint:** `internal/bootstrap/assets/` is a verbatim copy of tack-roles files (`zshrc.j2`, `vimrc.j2`, `mcp-sync` and the MCP catalog). `task bootstrap:sync` runs `rm -rf` on it before copying, so pmox cannot keep its own files there.

**The review:** five reviewers (junior developer, senior developer, Linux admin, DevOps/SRE, platform/Kubernetes) produced the findings this change acts on. Their mise names were checked against `mise registry` (mise 2026.7.0), and the uncertain ones are listed under Open Questions.

## Goals / Non-Goals

**Goals:**
- Tools that get installed also get switched on (shell hooks, git pager).
- Fix the confirmed bugs in the script and the bootcmd.
- Offer system tuning and hardening as explicit opt-ins. Nothing new is preselected.
- Keep the picker easy to scan as the catalog grows, through tab split, clear labels and grouped rows.
- Keep re-runs idempotent and saved answers forward-compatible.

**Non-Goals:**
- No new MCP catalog specs. They belong in tack-roles and arrive through sync.
- No GPU passthrough for Ollama. That would be pmox VM-config work, not devbox-setup.
- No checksum pinning for every release download in this change. It is tracked as a follow-up; zellij moving to mise removes one unpinned download.
- No change to `picker.py` beyond what new tabs need, which is nothing.
- No Debian or jammy support work. noble stays the target; on other releases the `cli` item degrades gracefully (see Decisions).

## Decisions

1. **pmox-owned config lives in `internal/bootstrap/conf/`, embedded next to `assets/`.**
   - It lands on the VM at `$SHARE/conf/`. Its files include `shell.sh` (the zsh/bash drop-in), `tmux.conf`, `gitignore_global`, `daemon.json`, `sysctl-devbox.conf` and the sshd/journald/zram drop-ins.
   - Alternative rejected: editing `assets/zshrc.tmpl`. The next sync would wipe it, and it would drift from tack-roles.

2. **Activating the shell tools uses a drop-in, not a rewritten rc file.**
   - `install_shellcfg` copies `conf/shell.sh` to `~/.config/devbox/shell.sh`. It adds one guarded line (`[ -r ~/.config/devbox/shell.sh ] && . ~/.config/devbox/shell.sh`) to the end of `.zshrc` and `.bashrc`, once each.
   - The drop-in checks for each tool before using it (`command -v zoxide && eval "$(zoxide init $shell)"`), so it is safe whatever else was picked.
   - fzf bindings come from `/usr/share/doc/fzf/examples/key-bindings.{zsh,bash}`. noble's fzf 0.44 has no `fzf --zsh`.
   - Because `install_shell` rewrites `.zshrc` from the template, it also re-appends the source line when `shellcfg` is in the plan.
   - zsh-autosuggestions and zsh-syntax-highlighting are cloned into `custom/plugins`. They are enabled by appending them to the template's `plugins=()` through the existing sed substitution of `ZSH_PLUGINS`. The pmox-side `ZSH_PLUGINS` variable gains them and is proposed upstream to tack-roles.

3. **Git defaults are `git config --global` calls, not a written `.gitconfig`.**
   - This merges with the identity that `gitid` and `github` write.
   - delta settings are applied only when `delta` is on PATH.
   - The global ignore is written to `~/.config/git/ignore` only if that file is absent.

4. **Tab layout:**
   - The `SCREENS` order becomes System · Shell & CLI · Dev tools · Languages · Cloud · IaC · Kubernetes · AI · Accounts.
   - The `screen` field changes for existing rows such as `terraform` (cloud→iac) and `k8s` (cloud→k8s). Ids stay the same, so saved answers are unaffected.
   - New rows prefer one row per purpose over one row per binary, so the lists stay short.

5. **Version and source changes:**
   - The `editors` row becomes apt `vim` only.
   - A new `neovim` row uses mise `neovim@latest`. `load_answers` maps `editors` to `editors neovim`, so existing users keep Neovim.
   - `zellij` becomes mise `zellij@latest`. Old `/usr/local/bin/zellij` binaries are left alone, and mise's shim wins on PATH.
   - Python goes to `python@3.13`; Java goes to `java@temurin-25`.
   - Helm is pinned as `helm@3` until helmfile and helm-diff are verified on Helm 4.

6. **Opt-in security that interacts with pmox's auto-upgrade switch-off:**
   - The bootcmd becomes `[ -e /etc/devbox-setup/auto-upgrades ] || systemctl disable --now …`.
   - The `autoupdates` item creates that marker, rewrites `20auto-upgrades` with `1`/`1`, sets `Automatic-Reboot "false"` and enables the timers.
   - Alternative rejected: `cloud-init-per instance`. It would also stop the switch-off from re-applying after a user's manual change, and it is harder to test as a string.

7. **apt runs non-interactively without surprises:**
   - `APT_OPTS` adds `-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold`, and `90devbox-setup` gets the same so vendor scripts inherit it.
   - `apt_install` exports `NEEDRESTART_MODE=l`, so nothing restarts during the run. The closing summary lists what needrestart reports.
   - The `needrestart` item later sets quiet auto-restart for the user's own `apt upgrade`.

8. **Risky items guard themselves:**
   - `sshharden` refuses to apply when the user's `authorized_keys` is empty. It writes `10-devbox.conf`, which sorts before `50-cloud-init.conf`, and runs `sshd -t` before reloading.
   - `ufw` adds its SSH rule (plus tailscale0, mosh and mcpjungle when picked) before `ufw --force enable`. Its label warns that Docker's published ports bypass ufw.
   - `fail2ban` ignores `100.64.0.0/10` (Tailscale).

9. **AI agent wiring:**
   - `A_WIRE_CLAUDE` becomes `A_WIRE_AGENTS`; `load_answers` maps the old value.
   - When it is set, mcpjungle is registered with each picked agent that has a CLI or config: `claude mcp add`, `codex mcp add` or a `~/.codex/config.toml` entry, and `gemini mcp add --transport http`.
   - **ECC** (github.com/affaan-m/ecc) is an `fn` row that needs `node` and `claude`. It runs `npx --yes ecc-universal@<pinned> install --profile minimal --target claude` as the user.
     - It is pinned to a version, like gum and mcpjungle.
     - `--profile minimal` is used because it installs no hook runtime; the larger profiles add hooks and more modules. Verified on a VM, minimal still installs about 68 agents into `~/.claude/agents`, plus rules and commands. The label says so, and the source column names `npx ecc-universal uninstall` for removal.
     - Rejected: the guided `setup` subcommand, because it is interactive and the spinner steps run with no terminal.
   - **OpenSpec** is a mise row, `npm:@fission-ai/openspec@latest`, that needs `node`. `openspec init` stays per project, so devbox-setup installs only the CLI.
   - **`agentcfg`** writes `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`, each only if absent, describing the box: a throwaway Proxmox VM, tools come from mise, sudo is available, kubeconfig is in `~/.kube`.

10. **Saved-answer migration stays in `load_answers`.** The new mappings join the existing `case`: `editors` → `editors neovim`, plus the `A_WIRE_CLAUDE` rename. No answers file version is introduced.

## Risks / Trade-offs

- **Some mise names are unverified** (see Open Questions) → each uncertain name gets a smoke test on a noble VM before merge. Fall back to an `aqua:` or `ubi:` spec, or drop the tool.
- **The catalog grows to about 70 rows and nine tabs** → number keys 1–9 already jump to a tab. Labels stay short, and the source column now wraps.
- **ECC and agentcfg write into `~/.claude`, which the user may manage themselves** → both are opt-in. agentcfg never overwrites, and ECC keeps an install manifest with an uninstall command.
- **sshharden or ufw can lock the user out** → the guards above. Both are listed as "advanced" in their labels, and `--dry-run` shows the files they would write.
- **The bootcmd guard only reaches newly launched VMs** → existing VMs that opt into autoupdates will have them turned off again at the next boot. `devbox-setup` warns about this when the bootcmd is present without the guard: it greps `/var/lib/cloud/instance/user-data.txt`.
- **Docker `daemon.json` changes need a docker restart, which kills running containers** → write it only when absent, and restart docker only in the same run that installs it.

## Migration Plan

1. Land the bug fixes and `conf/` embedding first; they are independent and low-risk.
2. Shell and Git setup, then system tuning, then security items, then the catalog and tab split, then AI wiring. Each is a separate commit, so any one can be reverted.
3. Existing VMs: `sudo devbox-setup --yes` re-applies the saved picks with the fixes. New items need an interactive run.
4. Rollback: revert the commit. Settings already written to VMs remain; each `conf.d` drop-in is one file to delete.

## Open Questions

- **Names to verify with `mise registry` / `mise ls-remote` on noble:**
  - `hurl`, `usql`, `prek`
  - `bitwarden` and `1password-cli` (deferred unless verified)
  - `aqua:block/goose`, `aqua:cilium/hubble`
  - the `go:` backend ordering for gopls and delve within one `mise use -g` call
- Does `krew` through mise create `~/.krew` correctly, or does it need an `fn`?
- Does Helm 4 work with helm-diff without `--verify=false`? This decides when to lift the `helm@3` pin.
- Which ECC version to pin, and does `install --profile minimal` run headless with no prompts? Check `npx ecc-universal@<ver> install --help` before choosing.
- Should `essentials` be preselected? The review suggested it. This design keeps "nothing preselected", the earlier decision, and renames the item "Essentials (recommended)".
