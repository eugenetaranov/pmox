## 1. Slice 1 — core (Go owns UI and plan)

- [ ] 1.1 `cmd/devbox-setup`, `internal/devbox/*` skeleton; import-boundary test
- [ ] 1.2 `catalog`: TOML schema, embed, validation, `catalog.d`; convert CATALOG_ROWS to TOML (generator, then hand-tidy)
- [ ] 1.3 `answers`: answers.json v1 read/write, migration table (shared with profiles)
- [ ] 1.4 `plan`: resolve + catalog order (ports `resolve()`); tests from bash cases
- [ ] 1.5 `steps`: interface, fake exec for tests, `asUser`; `apt`, `apt_repo`, `mise`, `file`, `template`, `link`, `git_clone`, `systemctl`, `sysctl`, `user_shell`, `run`
- [ ] 1.6 Runner: batched apt, per-user mise, failure/skip semantics, log, spinner, summary file, `--dry-run`, `--verify`
- [ ] 1.7 `legacy` step: bundled bash run with `--only <id>`; add `--only` to the bash script
- [ ] 1.8 `ui`: multi-tab picker in `internal/tui`, search port (picker_test.py cases → Go), prompts (user, git, tz, MCP, mise extras), review
- [ ] 1.9 `profile`: format, `export`, `--profile` file/https, strict keys
- [ ] 1.10 Build: goreleaser + `task build` produce linux amd64/arm64 devbox-setup; pmox embeds them; push picks by `uname -m`
- [ ] 1.11 `pmox launch --devbox-profile`, `devbox_profile` config
- [ ] 1.12 `task devbox:e2e`: throwaway VM, profile with every item, `--verify`, re-run no-op, delete

## 2. Port items, tab by tab (each: checklist from spec + bash, unit tests, e2e, delete the bash function)

- [ ] 2.1 Shell & CLI (zsh, zshcfg, p10k, vimcfg, shellcfg, fzftab, tmuxcfg, cli)
- [ ] 2.2 Dev tools and Languages (docs, profiling, mise-only items)
- [ ] 2.3 Cloud, IaC, Kubernetes (krew, helmfile, k3s)
- [ ] 2.4 AI (claude, omp, ollama, ecc, agentcfg, mcpjungle + mcp-sync + agent wiring)
- [ ] 2.5 System (docker, tailscale, monitoring, devlimits, zram, journald, autoupdates, needrestart, sshharden, ufw, fail2ban)
- [ ] 2.6 Accounts (gitcfg, sshkey, github)

## 3. Cut-over

- [ ] 3.1 Remove the bash script, `picker.py`, `picker_test.py`, gum, the `legacy` step and `bootstrap.Tarball`
- [ ] 3.2 Reword devbox-* spec requirements that name bash functions; README, llms.txt
- [ ] 3.3 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-go-rewrite`, e2e on amd64 (and arm64 if a node is available)
