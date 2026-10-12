## 1. Delivery over SSH

- [x] 1.1 `bootstrap.Tarball()` (paths, modes, gzip tar) and `InstallCommand`; `Inject` keeps only the unattended-upgrades switch-off; drop `registryNames`
- [x] 1.2 Launch phase "Installing devbox-setup" after the boot wait, before hooks, via `Options.PushDevboxFn`; warning on failure; note with `--no-wait-ssh`
- [x] 1.3 Wire `PushDevboxFn` in `cmd/pmox` with the `pmox cp` SSH target (`sudo tar -xzf - -C /`)
- [x] 1.4 `pmox devbox push [vm]` (picker when no VM is given), help text, `cli-command-tree` grouping
- [x] 1.5 Tests: launch phase order, failure keeps the ready tag, user-data has no devbox files, tarball contents and modes

## 2. Answers as data

- [x] 2.1 `picker.py answers read|write` (legacy conversion on read), with the migration table moved there and `chrony` dropped with a note
- [x] 2.2 `load_answers` and `save_answers` call it; `printf -v` on known names only; remove the `.` of the file
- [x] 2.3 Tests: legacy → JSON round trip (spaces, quotes, `$'…'`, non-ASCII), tampered file runs nothing, `--yes` migration via bash

## 3. Downloads and registry

- [x] 3.1 gum SHA-256 pins per architecture; verify the temp file before extracting; test that the pins match a recorded `checksums.txt` fixture
- [x] 3.2 `.github/workflows/mise-registry.yaml`: weekly and dispatch, latest mise, ≥500-row guard, bot commit of the TSV only
- [x] 3.3 Ship the full TSV to the VM

## 4. Clock sync in pmox

- [x] 4.1 `bootstrap.ClockSync`: merge packages, write_files and runcmd into the user-data (no duplicates; the user's file is untouched)
- [x] 4.2 `clock_sync` server config (default on) and `--no-clock-sync`; config docs
- [x] 4.3 Remove the `chrony` row, `install_chrony` and `conf/chrony-pmox.conf` from devbox-setup
- [x] 4.4 Tests: merge with and without existing packages, opt-out, rendered runcmd

## 5. Sync ownership

- [x] 5.1 Move `zshrc.tmpl` and `vimrc` to `conf/`; update script paths and tests
- [x] 5.2 `bootstrap:sync` copies only `mcp-sync` and `mcp-catalog/` (no `rm -rf` of conf files)

## 6. Docs and checks

- [x] 6.1 README, llms.txt: push command, clock sync, `--no-wait-ssh` note
- [x] 6.2 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-decouple-prep`
- [x] 6.3 Live check on a VM: launch → files present; `devbox push` refresh; legacy answers converted; chrony stepping with `/dev/ptp0`
  - testvm: `pmox devbox push` replaced the script and share dir (/usr/local modes unchanged; full 1012-row registry); `--yes --dry-run` converted the legacy answers to answers.json field for field (old file kept as answers.old) and showed the same plan. State backed up first to /var/lib/devbox-setup.bak-p1.
  - Fresh launch (p1test, deleted afterwards): devbox-setup installed before ready; user-data 1,967 bytes with no devbox files; chrony selected PHC0 with the pmox drop-in; a 5-minute backward jump was stepped back (300 s) about 10 s later; `--yes` with `tmuxcfg` installed gum through the pinned checksum and wrote ~/.tmux.conf.
