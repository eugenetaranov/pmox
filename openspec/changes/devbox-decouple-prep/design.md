## Context

devbox-setup is ~1340 lines of bash plus `picker.py`, with `assets/` (synced from tack-roles) and `conf/` (pmox's). `bootstrap.Inject` adds everything to the user-data's `write_files`. User-data is ~61 KB, near PVE's 64 KiB snippet limit, so the mise registry is already shipped without descriptions. `load_answers` runs `. "$ANSWERS"` as root. `ensure_gum` pipes curl into tar. `install_chrony` is one of the System items.

This change keeps bash. It prepares the ground so the Go rewrite (`devbox-go-rewrite`) can replace the program without also changing how it gets to the VM or what it stores.

## Goals / Non-Goals

**Goals:** nothing devbox-related in user-data; an answers format that Go reads as-is; no unverified binaries; Proxmox-only behaviour in pmox.

**Non-Goals:** rewriting installers, presets/profiles (Phase 2), a separate repo or release.

## Decisions

- **Push after the boot wait, before hooks.** A new launch phase "Installing devbox-setup" runs after Phase 8b. It streams a gzip tar of the files over the launch's SSH connection to `sudo tar -xzf - -C /`. Running it before hooks lets a `--post-create` hook call `devbox-setup --yes`. As before, a failure is a warning, not a launch failure: the VM is fine without it, and `pmox devbox push` retries.
  - `launch.Options` gets a `PushDevboxFn`, like `WaitForSSHFn`, so tests stub it and `cmd/pmox` wires it to the same SSH target code `pmox cp` uses.
  - *Alternative:* keep user-data and compress harder. Rejected: it only delays the limit, and a Go binary won't fit at all.
- **One tarball builder.** `bootstrap.Tarball()` returns the files with their paths and modes, and both launch and `devbox push` use it. `Inject` and the size check in `withDevboxSetup` go away.
- **`pmox devbox push <vm>`** is a new `devbox` noun (noun-verb, as in `cli-command-tree`), so Phase 2 can add `devbox profile …` next to it. It needs sudo on the VM, like devbox-setup itself.
- **answers.json, handled by picker.py.** python3 is already a dependency, so `picker.py answers read` prints shell-safe `A_X=…` assignments, and `picker.py answers write` writes the JSON. The script uses `declare` on known names only and never runs `eval` or `.` on file content. Format:
  ```json
  {"version": 1, "user": "ubuntu", "picks": ["cli", "zsh"], "mise_extra": ["starship"],
   "mcp_listen": "", "mcp_servers": [], "wire_agents": true,
   "git": {"name": "", "email": ""}, "tz": "UTC"}
  ```
  - Lists are real lists. Phase 2's Go reads this file unchanged.
  - The id migrations (`shell` → `zsh zshcfg p10k`, and the rest) stay in one table, applied on read.
  - *Alternative:* a whitelisted `KEY=value` parser in bash. Rejected: decoding `printf %q` output safely in bash is fiddly, and Go would need yet another parser.
- **Legacy conversion:** when only the old `answers` file exists, picker.py decodes it line by line:
  - Keys must be one of `ANSWER_VARS`, and values use `%q` quoting (`\x`, `'…'`, `$'…'`).
  - Any other line is ignored with a warning.
  - It writes `answers.json`, then renames the old file to `answers.old`.
- **gum checksum:** `GUM_SHA256_amd64` / `GUM_SHA256_arm64` sit next to `GUM_VERSION`, taken from gum's `checksums.txt`. The script downloads to a temp file, runs `sha256sum -c`, and on a mismatch dies before extracting. A Go test checks that the pins match the version, using a fixture of the upstream checksums file recorded at pin time.
- **Registry workflow:** `.github/workflows/mise-registry.yaml` runs weekly and on manual dispatch.
  - It installs the latest mise, runs `task bootstrap:mise-registry`, and when the file changed, commits it to main as `github-actions[bot]` with `chore: refresh mise registry`.
  - The VM's live fetch from main (7-day cache) picks it up without a pmox release.
  - Per the decision, no pinning. A sanity check skips the commit when the new file has fewer than 500 rows, so a broken mise can't empty the list.
- **Full snapshot on the VM:** `registryNames()` is dropped. The VM gets names, aliases and descriptions, so search can show descriptions offline.
- **Clock sync in cloud-init:** a `clocksync` package merges the following into the user-data:
  - `packages: [chrony]`
  - write_files: `/etc/modules-load.d/ptp_kvm.conf` and `/usr/local/share/pmox/chrony-ptp.conf`
  - a `runcmd` that runs `modprobe ptp_kvm`, installs the drop-in only if `/dev/ptp0` exists, and restarts chrony

  The drop-in is installed only when the device exists because chronyd won't start with a refclock on a missing device.

  This is the same YAML merge `Inject` does today, about 600 bytes.
  - It is on by default; `clock_sync: false` (per server) or `--no-clock-sync` turns it off.
  - When the user's cloud-init already lists chrony or another time daemon (systemd-timesyncd excluded), pmox still adds the drop-in; packages are merged without duplicates.
- **Removing the chrony item:** the row and `install_chrony` go. In the migration table `chrony` maps to nothing, and the first load prints "chrony is now set up by pmox at launch; to add it to an existing VM run: …". VMs that already have it keep it.
- **Ownership (devbox is a parallel path to tack):** devbox-setup ships its own copy of every file it uses and never needs tack at run time.
  - `zshrc.tmpl` and `vimrc` have diverged ahead of tack-roles, so they move to `conf/` and devbox-setup owns them.
  - `mcp-sync` and `mcp-catalog/` stay synced from tack-roles. Both paths then install the same MCP servers the same way, so "the same result" holds where it matters most.
  - The sync task copies only those files. If tack-roles wants the newer zshrc, it copies from pmox.

## Risks / Trade-offs

- [Launches with `--no-wait-ssh` lose devbox-setup] → pmox prints a note with `pmox devbox push <vm>`; README says so.
- [The push needs SSH as a sudoer, but a custom cloud-init may create a user without sudo] → warn with the error, and leave the VM ready.
- [The bot commits to main] → it touches only one generated file, CI still runs, and it can be turned off by disabling the workflow.
- [Clock sync adds an apt install to first boot (~5–10 s)] → opt-out flag; pmox doesn't wait on it.
- [The answers rewrite could drop a field] → round-trip tests: legacy file → JSON → `A_*` values equal the old sourced values, for every `ANSWER_VARS` entry.

## Migration Plan

Old VMs keep working: their devbox-setup still reads the old file. On the new version's first run, the file is converted. Pushing to an old VM overwrites the script and share dir, the same files user-data wrote. Rollback: the previous pmox still injects via user-data.

## Open Questions

- Should `pmox devbox push` also run `devbox-setup --yes` when answers exist (`--apply`)? Proposed: yes, as an opt-in flag.
