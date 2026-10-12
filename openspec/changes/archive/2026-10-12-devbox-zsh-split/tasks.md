## 1. Catalog and install

- [x] 1.1 Rows `zsh`, `zshcfg`, `p10k` replace `shell`; fzftab needs `zshcfg cli`; shellcfg label
- [x] 1.2 `install_zsh`, `install_zshcfg` (theme follows the p10k clone), `install_p10k` (clone, theme line, preset if absent)
- [x] 1.3 `conf/p10k-devbox.zsh`; instant-prompt block in `zshrc.tmpl`
- [x] 1.4 Migration `shell` → `zsh zshcfg p10k`

## 2. Tests and docs

- [x] 2.1 Go tests (rows/needs, migration, p10k file, template); picker_test `zsh` top five
- [x] 2.2 README
- [x] 2.3 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-zsh-split`, user-data under 64 KiB
- [x] 2.4 Fresh VM: p10k-only install, zsh-only install, zshcfg re-run keeps theme, migration dry run
  - A fresh VM couldn't be started: PVE refused new guest memory ("cannot set up guest memory 'pc.ram'") despite free RAM. Tested on testvm with throwaway users instead (removed afterwards; saved answers restored):
    - p10k alone → zsh, zshcfg, p10k installed; login shell zsh; first zsh shows the one-line lean prompt with time, no wizard; transient prompt on; `POWERLEVEL9K_CONFIG_FILE` = ~/.p10k.zsh. Found and fixed: BATTERY_STAGES type error (array in the preset) that aborted the file.
    - zshcfg re-run keeps `ZSH_THEME="powerlevel10k/powerlevel10k"`.
    - zsh alone → login shell only, plus an empty ~/.zshrc (so zsh's new-user menu doesn't open).
    - Answers `cli shell fzftab` → plan cli, zsh, zshcfg, p10k, fzftab, applied with `--yes`.
