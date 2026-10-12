## Why

devbox-setup is a parallel path to tack: it gives the same result as the tack-roles playbooks (MCPJungle with MCP servers, dev tools set up and configured) without anyone maintaining roles.

devbox-setup is about to become its own tool (see `devbox-go-rewrite`). Four things tie it to pmox's cloud-init today and would block or complicate that, and two are security gaps:

- It travels in user-data, which is ~61 KB of PVE's 64 KiB snippet limit. Over the limit it is silently skipped, with only a stderr warning.
- Saved answers are a shell file that devbox-setup **sources as root**. Fine while only devbox-setup writes it; unsafe once answers can be shared as presets.
- gum is downloaded with no checksum.
- The chrony item is Proxmox-specific (the KVM PTP clock and snapshot rollbacks), so it belongs in pmox, not in a generic devbox tool.

Two more are housekeeping: the mise registry snapshot goes stale between releases, and `task bootstrap:sync` overwrites pmox's newer `zshrc.tmpl` with tack-roles' older copy.

## What Changes

- **Delivery over SSH:** after the VM answers SSH, pmox copies devbox-setup's files in one tar stream. It no longer goes in user-data. A new `pmox devbox push <vm>` installs or refreshes it on an existing VM. **BREAKING:** with `--no-wait-ssh`, devbox-setup isn't installed (pmox says so and prints the push command).
- **Answers as data:** answers move to `/var/lib/devbox-setup/answers.json` (versioned), read and written by a parser that never executes the file. The old shell-format file is converted once, also without running it.
- **Checksum-pinned downloads:** gum's tarball is checked against a pinned SHA-256 per architecture before install.
- **Registry kept fresh:** a weekly GitHub Action regenerates `mise-registry.tsv` from the latest mise and commits it to main. Not pinned. Since user-data is no longer the limit, the VM gets the full snapshot with descriptions.
- **Clock sync moves to pmox:** pmox adds chrony with the KVM PTP clock and `makestep 1 -1` to every launch's cloud-init, on by default (`clock_sync: false` in the config or `--no-clock-sync` to opt out). **BREAKING (catalog id):** the `chrony` item leaves devbox-setup; saved answers that include it skip it, with a note.
- **Sync stops clobbering:** `zshrc.tmpl` and `vimrc` move to `conf/`, which pmox owns. `bootstrap:sync` copies only `mcp-sync` and the MCP catalog, which tack-roles owns.

## Capabilities

### New Capabilities
- `devbox-delivery`: copying devbox-setup to a VM over SSH at launch, and `pmox devbox push`.
- `vm-clock-sync`: chrony + KVM PTP in pmox's cloud-init.

### Modified Capabilities
- `devbox-setup-runtime`: the answers data file, checksum-pinned downloads, and file ownership after the sync change.
- `devbox-system-tuning`: the `chrony` item is removed.
- `devbox-picker-search`: the registry snapshot is refreshed weekly and installed in full.

## Impact

- `internal/launch` (a new push phase replaces `withDevboxSetup`), `internal/bootstrap` (a tarball instead of write_files, `conf/` moves), `cmd/pmox` (`devbox push`, `--no-clock-sync`), `internal/config` (`clock_sync`)
- `devbox-setup` and `picker.py` (answers I/O, gum checksum, chrony removed)
- `.github/workflows/mise-registry.yaml` (new), `Taskfile.yml`, README, llms.txt
