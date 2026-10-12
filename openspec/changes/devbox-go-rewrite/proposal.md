## Why

devbox-setup is a parallel path to tack: anyone who doesn't want to maintain tack-roles playbooks runs one command and gets the same result, with MCPJungle and its MCP servers wired into the agents, and the dev tools installed and configured. It must stand alone, so nothing at run time depends on tack or tack-roles.

devbox-setup is 1340 lines of bash, a python3 curses picker, and gum for prompts. It has grown to 97 items, a dependency resolver, saved-answer migrations and fuzzy search, and those are the parts bash handles worst: string-matched lists, no types, tests that shell out to bash, and three runtimes on the VM. It is also meant to become its own tool, usable on AWS, Hetzner or DigitalOcean VMs, not only pmox ones. One static Go binary fixes both: no python3 or gum to install, a typed catalog that tests can check, and pmox's own TUI for the picker.

Depends on `devbox-decouple-prep` (SSH delivery, `answers.json`, and chrony moving to pmox) and `devbox-credentials-flow` (the ask-or-skip credential flow, GitHub modes, `forget`), which the port carries over unchanged.

A review by five developer personas (full-stack, AI-agent heavy, platform/SRE, team lead, security-minded) found gaps beyond credentials. Those that fit the rewrite are added here.

## What Changes

- **A Go `devbox-setup` binary** (linux amd64 and arm64, static) replaces the bash script, `picker.py` and gum. Same name and same `sudo devbox-setup`, `--yes` and `--dry-run` usage.
- **The catalog becomes data:** embedded TOML, one entry per item with its tab, label, needs and a list of declarative **steps** (apt, apt repo, mise, file, template, git clone, link, systemctl, sysctl, user shell, run). The ~12 items that need logic (Docker, Tailscale, MCPJungle, GitHub login, git defaults, and others) are Go step types. Local additions go in `/etc/devbox-setup/catalog.d/*.toml`.
- **Runner:** plans and orders by needs, stops dependents of a failed item, and prints a summary as today. `--dry-run` lists the exact packages, commands and files. `--verify` checks that picked items are present.
- **Profiles (presets):** `devbox-setup export` writes the current choices as a TOML profile. `devbox-setup --profile <file|URL>` loads one onto the review screen, and `pmox launch --devbox-profile <file>` pushes and applies one. A profile is data only (no commands) and holds no secrets.
- **Search and UI ported:** the tabbed picker, type-to-search across tabs and mise's registry, and the prompts, built on `internal/tui` (bubbletea/huh).
- **pmox delivery:** `pmox launch` and `pmox devbox push` copy the binary for the VM's architecture instead of the script tarball.
- **Credentials as a step type:** `cred` steps declare a file, mode, keys and how to get them, porting `CRED_ROWS` from `devbox-credentials-flow`. devbox-setup still never stores or ships a secret.
- **Profiles for teams:**
  - Layered profiles (`--profile team.toml --profile me.toml`).
  - A `user` key and `--user`.
  - Named profiles that *replace* their own earlier picks on re-apply, listing what they drop.
  - An optional version lock (`export --lock`).
  - `pmox devbox apply` and `pmox devbox verify` across many VMs.
- **Re-runs keep your own lines:** `~/.zshrc.local`, `~/.config/devbox/local.sh` and `~/.tmux.local.conf` are sourced, and never written.
- **Per-user state:** answers and the MCPJungle gateway are kept per user, so two users on one VM don't overwrite each other. The gateway runs as a separate `mcp` system user that owns the MCP env files.
- **New items:**
  - corporate proxy
  - internal CA bundle
  - dotfiles repo
  - unattended-agent settings (Claude, Codex)
  - gateway wiring for OpenCode and Goose
  - gateway auth, or binding to the Tailscale IP
  - kubelogin
  - extra ufw ports for dev servers
  - a weekly `mise up` timer
- **Supply chain:** every download is either pinned or listed as unverified in `~/devbox-setup.txt`. `apt_repo` requires a key fingerprint.
- **Team catalog from pmox:** `pmox devbox push` also ships `~/.config/pmox/devbox/catalog.d/*.toml` and `mcp-catalog/*.spec`.
- **BREAKING:** `bootstrap.Files`, `picker.py`, the gum install and the bash script are removed once every item is ported. Hand-editing `/usr/local/bin/devbox-setup` on a VM is replaced by `catalog.d` overrides.

## Capabilities

### New Capabilities
- `devbox-catalog-data`: the catalog format, step types and local overrides.
- `devbox-runner`: planning, ordering, failure handling, dry run, verify and the summary.
- `devbox-profiles`: exporting, loading and applying profiles.
- `devbox-cli`: the binary, its flags and its runtime dependencies.
- `devbox-catalog-additions`: the new items: proxy, CA, dotfiles, agent safety, more agent wiring, gateway auth, kubelogin, ufw dev ports, the update timer.

### Modified Capabilities
- `devbox-delivery`: pushes the binary for the VM's architecture, also ships team `catalog.d` and MCP specs, and adds `pmox devbox apply` and `pmox devbox verify` across VMs.
- `devbox-setup-runtime`: gum is gone; checksum pinning covers vendor scripts and release binaries the steps download. Also adds local override files, per-user state, the `mcp` service user, and the unverified-download list.

## Impact

- New `cmd/devbox-setup`, `internal/devbox/{catalog,plan,steps,answers,profile,ui}`
- `internal/bootstrap` shrinks to embedding the built binaries; `internal/tui` gains the multi-tab picker
- `.goreleaser.yaml` (linux-only devbox-setup build, its output embedded into pmox), Taskfile, CI (a VM integration job)
- Existing devbox-* specs: behaviour unchanged; the port must pass them
