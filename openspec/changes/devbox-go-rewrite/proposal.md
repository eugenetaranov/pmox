## Why

devbox-setup is a parallel path to tack: anyone who doesn't want to maintain tack-roles playbooks runs one command and gets the same result, with MCPJungle and its MCP servers wired into the agents, and the dev tools installed and configured. It must stand alone, so nothing at run time depends on tack or tack-roles.

devbox-setup is 1340 lines of bash, a python3 curses picker, and gum for prompts. It has grown to 97 items, a dependency resolver, saved-answer migrations and fuzzy search, and those are the parts bash handles worst: string-matched lists, no types, tests that shell out to bash, and three runtimes on the VM. It is also meant to become its own tool, usable on AWS, Hetzner or DigitalOcean VMs, not only pmox ones. One static Go binary fixes both: no python3 or gum to install, a typed catalog that tests can check, and pmox's own TUI for the picker.

Depends on `devbox-decouple-prep` (SSH delivery, `answers.json`, and chrony moving to pmox).

## What Changes

- **A Go `devbox-setup` binary** (linux amd64 and arm64, static) replaces the bash script, `picker.py` and gum. Same name and same `sudo devbox-setup`, `--yes` and `--dry-run` usage.
- **The catalog becomes data:** embedded TOML, one entry per item with its tab, label, needs and a list of declarative **steps** (apt, apt repo, mise, file, template, git clone, link, systemctl, sysctl, user shell, run). The ~12 items that need logic (Docker, Tailscale, MCPJungle, GitHub login, git defaults, and others) are Go step types. Local additions go in `/etc/devbox-setup/catalog.d/*.toml`.
- **Runner:** plans and orders by needs, stops dependents of a failed item, and prints a summary as today. `--dry-run` lists the exact packages, commands and files. `--verify` checks that picked items are present.
- **Profiles (presets):** `devbox-setup export` writes the current choices as a TOML profile. `devbox-setup --profile <file|URL>` loads one onto the review screen, and `pmox launch --devbox-profile <file>` pushes and applies one. A profile is data only (no commands) and holds no secrets.
- **Search and UI ported:** the tabbed picker, type-to-search across tabs and mise's registry, and the prompts, built on `internal/tui` (bubbletea/huh).
- **pmox delivery:** `pmox launch` and `pmox devbox push` copy the binary for the VM's architecture instead of the script tarball.
- **BREAKING:** `bootstrap.Files`, `picker.py`, the gum install and the bash script are removed once every item is ported. Hand-editing `/usr/local/bin/devbox-setup` on a VM is replaced by `catalog.d` overrides.

## Capabilities

### New Capabilities
- `devbox-catalog-data`: the catalog format, step types and local overrides.
- `devbox-runner`: planning, ordering, failure handling, dry run, verify and the summary.
- `devbox-profiles`: exporting, loading and applying profiles.
- `devbox-cli`: the binary, its flags and its runtime dependencies.

### Modified Capabilities
- `devbox-delivery`: pushes the binary for the VM's architecture.
- `devbox-setup-runtime`: gum is gone; checksum pinning covers vendor scripts and release binaries the steps download.

## Impact

- New `cmd/devbox-setup`, `internal/devbox/{catalog,plan,steps,answers,profile,ui}`
- `internal/bootstrap` shrinks to embedding the built binaries; `internal/tui` gains the multi-tab picker
- `.goreleaser.yaml` (linux-only devbox-setup build, its output embedded into pmox), Taskfile, CI (a VM integration job)
- Existing devbox-* specs: behaviour unchanged; the port must pass them
