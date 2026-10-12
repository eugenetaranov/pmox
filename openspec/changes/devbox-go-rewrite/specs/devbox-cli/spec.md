## ADDED Requirements

### Requirement: Single static binary
devbox-setup SHALL be a static Go binary for linux amd64 and arm64, with its catalog, config files, templates, MCP catalog, mcp-sync and mise registry snapshot embedded. It SHALL NOT need python3, gum or bash scripts at run time.

#### Scenario: Minimal image
- **WHEN** devbox-setup runs on Ubuntu cloud image without python3
- **THEN** the picker opens and a run with `cli` picked completes

### Requirement: Compatible usage
`sudo devbox-setup` SHALL open the picker, `--yes` SHALL re-apply saved answers without questions, and `--dry-run` SHALL ask and then show the plan, as the bash version did. It SHALL fail with the same messages when there is no terminal and no `--yes`, or no saved answers with `--yes`.

#### Scenario: No answers with --yes
- **WHEN** `sudo devbox-setup --yes` runs on a VM with no saved answers
- **THEN** it exits non-zero saying there are no saved choices yet

### Requirement: Picker parity
The Go picker SHALL keep the bash picker's tabs, keys and search: number keys pick a tab only while no query is typed; typing searches all tabs and the mise registry; Esc clears the query or quits when it is empty; Enter on search results goes to Review. The search ranking SHALL match `devbox-picker-search`, including that `zsh` lists zsh, zsh config, Powerlevel10k, fuzzy tab completion and shell niceties first.

#### Scenario: Search zsh
- **WHEN** the user types `zsh`
- **THEN** the first five results are the items zsh, zshcfg, p10k, fzftab and shellcfg, with zsh first

### Requirement: Independent of pmox internals
Packages under `internal/devbox` and `cmd/devbox-setup` SHALL NOT import pmox's PVE, config or launch packages. Only `internal/tui` and the standard library and third-party modules are allowed.

#### Scenario: Import check
- **WHEN** the test suite runs
- **THEN** a test fails if any devbox package imports `internal/pveclient`, `internal/config` or `internal/launch`
