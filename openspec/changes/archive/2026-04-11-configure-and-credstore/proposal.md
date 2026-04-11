## Why

Every pmox command except `version` needs API credentials and a server
URL. Slice 1 ships a binary that prints version info and nothing else;
this slice ships the *only* command that produces useful state on disk
— `pmox configure` — plus the two internal packages every later slice
will read from: `internal/config` (YAML) and `internal/credstore`
(keychain wrapper).

Doing this as a dedicated slice keeps later command slices small. Once
this lands, every subsequent command slice (`launch`, `list`, etc.) can
assume "credentials and server selection just work" and focus on the
PVE API behavior it actually adds.

## What Changes

- Add `pmox configure` as the first real subcommand, wired into
  `cmd/pmox/main.go` (interactive, not a flag soup).
- Add `internal/config/config.go` — YAML loader and writer for
  `~/.config/pmox/config.yaml` (respecting `XDG_CONFIG_HOME`), with a
  flat per-server schema and `0600` file mode.
- Add `internal/credstore/credstore.go` — thin wrapper around
  `github.com/zalando/go-keyring` with `Get(url)`, `Set(url, secret)`,
  `Remove(url)`. No `List()` — go-keyring can't enumerate on macOS, so
  the config file is the source of truth for which servers exist.
- Add a minimal `internal/pveclient` that knows how to do exactly one
  thing: `GET /version` for credential validation. Full client lands in
  the `pveclient-core` slice. This slice ships only what configure
  needs.
- Add auto-discovery API calls during configure (`/nodes`,
  `/nodes/{node}/qemu`, `/nodes/{node}/storage`,
  `/nodes/{node}/network`) that populate picker menus with free-text
  fallback if any call fails.
- Add `--list` flag to `pmox configure` (lists configured server URLs
  from the config file, no secrets).
- Add `--remove <url>` flag to `pmox configure` (removes both the
  config-file entry and the keychain secret).
- Add table-driven unit tests for: URL canonicalization, token-ID
  format validation, YAML round-trip, credstore Get/Set/Remove against
  a fake keychain backend, and overwrite-prompt logic.

## Capabilities

### New Capabilities
- `configure-and-credstore`: a `pmox configure` command, a YAML config
  schema, and a keychain-backed credential store, sufficient for any
  later command to load credentials for a given server URL.

### Modified Capabilities
- `project-skeleton`: `cmd/pmox/main.go` gains its first real
  subcommand. The `init()` block adds `rootCmd.AddCommand(configureCmd)`
  and the `configure.go` sibling file (or inline definition — see
  design D2).

## Impact

- **New files**: `internal/config/config.go` + tests,
  `internal/credstore/credstore.go` + tests,
  `internal/pveclient/version.go` (the one endpoint configure needs) +
  tests, `cmd/pmox/configure.go` (or additions to `main.go` per D2).
- **New dependencies**: `github.com/zalando/go-keyring`,
  `golang.org/x/term` (for masked password prompts — already in tack's
  dep tree).
- **No new CI changes**: existing `build`, `test`, `lint` jobs from
  slice 1 cover this slice without modification.
- **Cross-slice contract**: this slice implements decisions
  [D-T2 (lazy snippet validation)](../../decisions.md#d-t2-snippet-storage-validation-is-lazy)
  — configure does **not** check snippet support — and produces the
  surface that
  [D-T4 (server resolution)](../../decisions.md#d-t4-server-resolution-logs-the-chosen-server-at--v)
  builds on (the next slice, `server-resolution`, reads the config map
  exposed by `internal/config`).
