## 1. Delivery over SSH

- [ ] 1.1 `bootstrap.Tarball()` (paths, modes, gzip tar); drop `Inject`, `registryNames` and the user-data size check
- [ ] 1.2 Launch phase "Installing devbox-setup" after the boot wait, before hooks, via `Options.PushDevboxFn`; warning on failure; note with `--no-wait-ssh`
- [ ] 1.3 Wire `PushDevboxFn` in `cmd/pmox` with the `pmox cp` SSH target (`sudo tar -xzf - -C /`)
- [ ] 1.4 `pmox devbox push [vm]` (picker when no VM is given), help text, `cli-command-tree` grouping
- [ ] 1.5 Tests: launch phase order, failure keeps the ready tag, user-data has no devbox files, tarball contents and modes

## 2. Answers as data

- [ ] 2.1 `picker.py answers read|write|convert`, with the migration table moved there and `chrony` dropped with a note
- [ ] 2.2 `load_answers` and `save_answers` call it; `declare` on known names only; remove the `.` of the file
- [ ] 2.3 Tests: legacy → JSON round trip (spaces, quotes, `$'…'`, non-ASCII), tampered file runs nothing, `--yes` migration via bash

## 3. Downloads and registry

- [ ] 3.1 gum SHA-256 pins per architecture; verify the temp file before extracting; test that the pins match a recorded `checksums.txt` fixture
- [ ] 3.2 `.github/workflows/mise-registry.yaml`: weekly and dispatch, latest mise, ≥500-row guard, bot commit of the TSV only
- [ ] 3.3 Ship the full TSV to the VM

## 4. Clock sync in pmox

- [ ] 4.1 `internal/clocksync`: merge packages, write_files and runcmd into the user-data (no duplicates; the user's file is untouched)
- [ ] 4.2 `clock_sync` server config (default on) and `--no-clock-sync`; config docs
- [ ] 4.3 Remove the `chrony` row, `install_chrony` and `conf/chrony-pmox.conf` from devbox-setup
- [ ] 4.4 Tests: merge with and without existing packages, opt-out, rendered runcmd

## 5. Sync ownership

- [ ] 5.1 Move `zshrc.tmpl` and `vimrc` to `conf/`; update script paths and tests
- [ ] 5.2 `bootstrap:sync` copies only `mcp-sync` and `mcp-catalog/` (no `rm -rf` of conf files)

## 6. Docs and checks

- [ ] 6.1 README, llms.txt: push command, clock sync, `--no-wait-ssh` note
- [ ] 6.2 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-decouple-prep`
- [ ] 6.3 Live check on a VM: launch → files present; `devbox push` refresh; legacy answers converted; chrony stepping with `/dev/ptp0`
