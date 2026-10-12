## 1. Picker

- [x] 1.1 `picker.py`: `M|` rows, query state, fuzzy scorer, search view, keys (type, backspace, esc clear/quit, ctrl-c), dedupe against catalog mise sources, `mise:<name>` ids
- [x] 1.2 `picker_test.py` (scorer ordering, parsing, dedupe) run from a Go test

## 2. devbox-setup

- [x] 2.1 Registry rows: live `mise registry -J --hide-aliased`, else the snapshot
- [x] 2.2 Split `mise:` picks into `RUN_MISE_EXTRA`; prefill the extra tools prompt
- [x] 2.3 Snapshot beside picker.py (`bootstrap:sync` replaces `assets/`); names-only copy embedded (user-data 64 KiB limit); full one fetched from GitHub at run time; `bootstrap:mise-registry` task

## 3. Docs and checks

- [x] 3.1 README (llms.txt has no devbox-setup section)
- [x] 3.2 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-picker-search`
- [x] 3.3 Drive the picker in a pty locally; dry-run and one real install on a VM
  - Locally in a pty: `zsh` → fzf-tab, zsh + oh-my-zsh, shell niceties, then mise tools; `find` finds `fd`; `k9s` → the Kubernetes CLIs item only; Esc clears, Esc again quits; Enter → Review; out = `fzftab mise:starship`.
  - testvm, `sudo devbox-setup --dry-run`: live registry; `starsh` → starship; the extra-tools prompt prefilled `starship`; the plan shows mise + `mise use -g starship`. With mise hidden and the GitHub copy not yet pushed, search fell back to the names-only copy.
  - The fetch from GitHub can only be checked once the snapshot is on `main`.
