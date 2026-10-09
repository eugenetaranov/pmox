## Purpose

The tabs, items, labels and install sources devbox-setup's picker
offers, and how the MCPJungle gateway is wired into the installed
coding agents.
## Requirements
### Requirement: Tabs
The picker SHALL show these tabs in order, followed by Review: **System · Shell & CLI · Dev tools · Languages · Cloud · IaC · Kubernetes · AI · Accounts**. Moving an existing item to a new tab SHALL keep its id.

#### Scenario: Tab order
- **WHEN** devbox-setup opens the picker
- **THEN** the tab bar reads System, Shell & CLI, Dev tools, Languages, Cloud, IaC, Kubernetes, AI, Accounts, Review

#### Scenario: Moved item keeps answers
- **WHEN** saved answers contain `terraform` and `k8s`
- **THEN** both are ticked, on the IaC and Kubernetes tabs respectively

### Requirement: Catalog items
In addition to the system, shell and security items in the other capabilities, the catalog SHALL offer the items below. Each SHALL install with the listed method and spec, and depend on the listed items.

| id | tab | label | how · what | needs |
|---|---|---|---|---|
| neovim | Shell & CLI | Neovim (latest) | mise `neovim@latest` | |
| zellij | Shell & CLI | zellij (tmux alternative) | mise `zellij@latest` | |
| mosh | Shell & CLI | mosh (SSH that survives sleep) | apt `mosh` | |
| docs | Shell & CLI | Man pages, completion, tldr | apt `man-db manpages-dev bash-completion command-not-found plocate tealdeer`; runs `unminimize` on minimal images (fn) | |
| lazygit | Dev tools | lazygit (git TUI) | mise `lazygit@latest` | |
| runners | Dev tools | just + watchexec + hyperfine | mise `just@latest watchexec@latest hyperfine@latest` | |
| http | Dev tools | HTTP clients (xh, hurl) | mise `xh@latest hurl@latest` | |
| lint | Dev tools | Linters + hooks (shellcheck, shfmt, actionlint, gitleaks, pre-commit) | mise `shellcheck@latest shfmt@latest actionlint@latest gitleaks@latest uv@latest pipx:pre-commit` | |
| dbclients | Dev tools | DB clients (psql, mariadb, sqlite3, redis-cli) | apt `postgresql-client mariadb-client sqlite3 redis-tools` | |
| pgcli | Dev tools | pgcli + usql | mise `uv@latest pipx:pgcli usql@latest` | |
| profiling | Dev tools | Profiling (perf, valgrind, bpftrace) | fn: apt `linux-tools-generic linux-tools-$(uname -r) valgrind bpftrace bpfcc-tools` | |
| act | Dev tools | act (run GitHub Actions locally) | mise `act@latest` | docker |
| ctools | System | Container tools (lazydocker, dive, hadolint, crane, cosign) | mise `lazydocker@latest dive@latest hadolint@latest crane@latest cosign@latest` | docker |
| netfs | System | NFS + SMB clients | apt `nfs-common cifs-utils autofs` | |
| monitoring | System | node_exporter (localhost) + glances | fn: apt `prometheus-node-exporter` listening on 127.0.0.1:9100; mise `pipx:glances` | |
| hcloud | Cloud | Hetzner Cloud CLI | mise `hcloud@latest` | |
| cloudflared | Cloud | cloudflared (Cloudflare tunnels) | mise `cloudflared@latest` | |
| backup | Cloud | restic + rclone | mise `restic@latest rclone@latest` | |
| iaclint | IaC | Terraform helpers (terragrunt, tflint, terraform-docs) | mise `terragrunt@latest tflint@latest terraform-docs@latest` | |
| iacscan | IaC | IaC and image scanners (trivy, checkov) | mise `trivy@latest uv@latest pipx:checkov` | |
| vault | IaC | Vault CLI | mise `vault@latest` | |
| k8sextra | Kubernetes | K8s extras (stern, kustomize, kubecolor, kubie, kubeconform) | mise `stern@latest kustomize@latest kubecolor@latest kubie@latest kubeconform@latest` | k8s |
| krew | Kubernetes | kubectl krew + plugins (neat, tree, view-secret) | fn | k8s |
| helmfile | Kubernetes | helmfile + helm-diff | fn: mise `helmfile@latest`, then `helm plugin install` helm-diff | k8s |
| gitops | Kubernetes | GitOps CLIs (Argo CD, Flux) | mise `argocd@latest flux2@latest` | k8s |
| k3d | Kubernetes | k3d (k3s in Docker) | mise `k3d@latest` | docker |
| k3s | Kubernetes | k3s cluster on this VM (systemd) | fn: get.k3s.io, kubeconfig copied to `~/.kube/config` mode 600 | |
| devloop | Kubernetes | Inner loop (Tilt, Skaffold, ctlptl) | mise `tilt@latest skaffold@latest ctlptl@latest` | k8s |
| talos | Kubernetes | talosctl | mise `talosctl@latest` | |
| opencode | AI | OpenCode | mise `opencode@latest` | |
| aider | AI | Aider | mise `uv@latest pipx:aider-chat[uvx_args=--python=3.12]` (pydub needs `audioop`, removed in Python 3.13) | |
| goose | AI | Goose | mise `aqua:block/goose@latest` | |
| ollama | AI | Ollama (local models; CPU-only without GPU passthrough, large) | fn: ollama.com/install.sh, listens on 127.0.0.1 | |
| openspec | AI | OpenSpec (spec-driven development CLI) | mise `npm:@fission-ai/openspec@latest` | node |
| ecc | AI | ECC for Claude Code (~68 agents, rules, commands; no hooks) | fn: `npx --yes ecc-universal@<pinned> install --profile minimal --target claude` as the user | node claude |
| agentcfg | AI | Describe this VM to agents (CLAUDE.md, AGENTS.md) | fn: writes `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md` only if absent | |
| glab | Accounts | GitLab CLI (glab) | mise `glab@latest` | |

#### Scenario: Every item resolves
- **WHEN** a test VM runs `devbox-setup --yes` with every catalog id picked
- **THEN** every step succeeds, or any failure is recorded in `FAILED` with no effect on unrelated items

#### Scenario: Dependencies pulled in
- **WHEN** only `ecc` is picked
- **THEN** the plan also contains `mise`, `node` and `claude`, ordered before `ecc`

### Requirement: Language toolchains with their usual companions
The language rows SHALL install:
- **go:** `go@latest golangci-lint@latest`, plus gopls, delve and govulncheck through the `go:` backend
- **python:** `python@3.13 uv@latest ruff@latest`
- **rust:** `rust@stable` and `cargo-binstall@latest`, followed by `rustup component add rust-analyzer` (fn)
- **java:** `java@temurin-25 maven@latest gradle@latest kotlin@latest`

The `k8s` row SHALL install `helm@3` instead of `helm@latest`.

#### Scenario: Go editor support
- **WHEN** `go` is installed
- **THEN** `gopls version` and `dlv version` succeed in a new shell

#### Scenario: Rust analyzer
- **WHEN** `rust` is installed
- **THEN** `rust-analyzer --version` succeeds

### Requirement: Self-explanatory labels
Every label SHALL say what the item is, without the user needing to know the tool's name. Items that download more than about 500 MB or build from source SHALL say "large" or give a time estimate. These relabels apply:
- `cli` → "CLI upgrades (ripgrep, fd, bat, eza, fzf, zoxide, delta, direnv)"
- `task` → "Task (Makefile alternative)"
- `omp` → "omp (oh-my-pi coding agent)"
- `docker` → "Docker Engine + Compose", with the docker-group note kept in the source column
- `essentials` → "Essentials (recommended)"
- `shell` → "zsh + oh-my-zsh + p10k (prompt wizard on first login)"
- `azure` and `gcloud` → "(large)" appended

#### Scenario: Large items marked
- **WHEN** the user views the Cloud tab
- **THEN** the Azure CLI and Google Cloud CLI labels include "(large)"

### Requirement: MCP gateway wired into picked agents
When mcpjungle is installed and the user agrees to wiring (`A_WIRE_AGENTS=yes`), devbox-setup SHALL register the mcpjungle endpoint with each picked agent that has a supported mechanism:
- Claude Code: `claude mcp add --scope user`
- Codex: its MCP config in `~/.codex/config.toml`
- Gemini CLI: `gemini mcp add --transport http`

Each registration SHALL be skipped when that agent already has an `mcpjungle` entry.

#### Scenario: Codex and Claude
- **WHEN** `claude`, `codex` and `mcpjungle` are installed with wiring on
- **THEN** both `claude mcp get mcpjungle` and Codex's config show the mcpjungle URL

#### Scenario: Re-run
- **WHEN** devbox-setup runs again with the same picks
- **THEN** no duplicate mcpjungle entries are created
