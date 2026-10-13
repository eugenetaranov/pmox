## 1. Slice 1 — core (Go owns UI and plan)

- [x] 1.1 `cmd/devbox-setup`, `internal/devbox/*` skeleton; import-boundary test
- [x] 1.2 `catalog`: TOML schema, embed, validation, `catalog.d`; convert CATALOG_ROWS to TOML (generator, then hand-tidy)
- [x] 1.3 `answers`: answers.json v1 read/write, migration table (shared with profiles)
- [x] 1.4 `plan`: resolve + catalog order (ports `resolve()`); tests from bash cases
- [x] 1.5 `steps`: interface, fake exec for tests, `asUser`; `apt`, `apt_repo`, `mise`, `file`, `template`, `link`, `git_clone`, `systemctl`, `sysctl`, `user_shell`, `run`
- [x] 1.6 Runner: batched apt, per-user mise, failure/skip semantics, log, spinner, summary file, `--dry-run`, `--verify`
- [x] 1.7 `legacy` step: bundled bash run with `--only <id>`; add `--only` to the bash script
- [x] 1.8 `ui`: multi-tab picker in `internal/tui`, search port (picker_test.py cases → Go), prompts (user, git, tz, MCP, mise extras), review
- [x] 1.9 `profile`: format, `export`, `--profile` file/https, strict keys
- [x] 1.10 Build: goreleaser + `task build` produce linux amd64/arm64 devbox-setup; pmox embeds them; push picks by `uname -m`
- [x] 1.11 `pmox launch --devbox-profile`, `devbox_profile` config
- [x] 1.12 `task devbox:e2e`: throwaway VM, profile with every item, `--verify`, re-run no-op, delete

## 2. Port items, tab by tab (each: checklist from spec + bash, unit tests, e2e; the bash functions stay until 4.1, as bash is the fallback on other architectures)

- [x] 2.1 Shell & CLI (zsh, zshcfg, p10k, vimcfg, shellcfg, fzftab, tmuxcfg, cli, docs)
- [x] 2.2 Dev tools and Languages (profiling, mise-only items)
- [x] 2.3 Cloud, IaC, Kubernetes (krew, helmfile, k3s)
- [x] 2.4 AI (claude, omp, ollama, ecc, agentcfg, mcpjungle + mcp-sync + agent wiring); agentkeys moves with the cred step (3.1)
- [x] 2.5 System (debug, docker, tailscale, monitoring, devlimits, zram, journald, tzlocale, autoupdates, needrestart, sshharden, ufw, fail2ban); registries moves with the cred step (3.1)
- [x] 2.6 Accounts (gitid, gitcfg, sshkey)
- [ ] 2.7 GitHub login and commit signing (github, gitsign): interactive gh flows, key uploads; needs a live GitHub account to test

## 3. Developer-review additions (each lands in the slice of its tab; listed here to track)

- [x] 3.1 `cred` step type porting `CRED_ROWS` (agentkeys, npmrc; registries and agekey as Go steps); reject secret keys in profiles
- [x] 3.2 Layered `--profile`, named layers that replace, `user` / `--user` (create the account), review lists dropped items
- [x] 3.3 `export --lock` and installing from `[lock]` (mise exact, apt best effort with a warning)
- [x] 3.4 Source the local override files from zshrc, shell.sh, tmux.conf and vimrc; README section
- [x] 3.5 Per-user state (system.json, ~/.config/devbox/answers.json, split from answers.json); `mcpjungle@<user>` with port allocation
- [x] 3.6 `mcp-<user>` service user owning the MCP env files; sudo-mediated edits; opt-in `sudopw`
- [x] 3.7 Require `key_fingerprint` on `apt_repo` and `sha256` or `unverified` on downloads; "Installed without a pinned checksum" list
- [ ] 3.8 Items: proxy, corpca, dotfiles, agentsafe, OpenCode and Goose wiring, gateway listen options, kubelogin, ufw dev ports, autoupdate-tools, docker-group editor note
- [ ] 3.9 pmox: ship `catalog.d` and MCP specs from `~/.config/pmox/devbox/` on push; `pmox devbox apply` and `pmox devbox verify` across VMs (4 at a time)

## 4. Cut-over

- [ ] 4.1 Remove the bash script, `picker.py`, `picker_test.py`, gum, the `legacy` step and `bootstrap.Tarball`
- [ ] 4.2 Reword devbox-* spec requirements that name bash functions; README, llms.txt
- [ ] 4.3 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-go-rewrite`, e2e on amd64 (and arm64 if a node is available)
