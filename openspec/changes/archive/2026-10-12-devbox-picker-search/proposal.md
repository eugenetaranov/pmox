## Why

devbox-setup's picker spreads ~70 items over nine tabs, so finding one means guessing its tab. Tools outside the catalog can only be added by typing names blind into the "Extra mise tools" prompt, with no way to browse what mise offers.

## What Changes

- Typing in the picker fuzzy-searches every tab at once. Results show each item's category, space toggles, Esc clears, and Esc on an empty query quits; `q` now types.
- The search also covers mise's registry (~940 tools, with descriptions): the live `mise registry` when mise is installed, otherwise pmox's snapshot fetched from GitHub, or the names-only copy pmox ships (the full one doesn't fit in user-data). Tools a catalog item already installs are hidden.
- Registry tools picked in the search are installed with `mise use -g` and prefill the "Extra mise tools" prompt, where versions can still be edited.
- A Taskfile task regenerates the snapshot.

## Capabilities

### New Capabilities
- `devbox-picker-search`: search across picker tabs and mise's registry, and how picked registry tools are installed.

### Modified Capabilities

## Impact

- `internal/bootstrap/picker.py`, `internal/bootstrap/devbox-setup`, `internal/bootstrap/bootstrap.go`
- New `internal/bootstrap/mise-registry.tsv` (~75 KB in the repo; a ~10 KB names-only copy embedded), new `bootstrap:mise-registry` task
- README
