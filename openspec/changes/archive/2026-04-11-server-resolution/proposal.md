## Why

Slice 2 (`configure-and-credstore`) ships the ability to store *many*
servers in `~/.config/pmox/config.yaml`. Every pmox command except
`configure` and `version` now needs a deterministic way to decide
*which* configured server a given invocation should target — and to do
so whether the user is running interactively, scripted in CI, or with
exactly one server configured.

Landing this as a dedicated slice keeps every later command slice
(`launch`, `list`, `info`, ...) from re-inventing server selection.
Once this lands, handlers can assume "call `server.Resolve(ctx, ...)`,
get back a URL + `*config.Server` + secret, move on."

## What Changes

- Add `internal/server` — a resolver package with a single
  `Resolve(ctx, opts) (*Resolved, error)` entrypoint that implements
  the five-step precedence ladder:
  1. `--server <url>` flag
  2. `PMOX_SERVER` env var
  3. exactly one configured server
  4. interactive picker (TTY only)
  5. error
- Add `--server` as a **persistent flag** on `rootCmd` and wire
  `PMOX_SERVER` env lookup into the resolver. `configure` ignores both.
- Extract the existing `selectOne` huh-based picker from
  `cmd/pmox/configure.go` into a new `internal/tui/picker.go` so the
  resolver's interactive branch can reuse it. `configure.go` imports
  the extracted helper — no behavior change there.
- The resolver's `Resolved` struct bundles canonical URL, the
  `*config.Server` block, and the secret pulled from `credstore`. A
  missing-secret keychain error is surfaced as a resolver error with a
  "re-run `pmox configure`" hint.
- Input matching for `--server` / `PMOX_SERVER`: parse as URL
  (prepending `https://` if no scheme is present), canonicalize via
  `config.CanonicalizeURL`, then exact-match against the config map.
  No hostname prefix or substring matching — a failed match is an
  error listing the configured URLs.
- TTY detection via `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))`.
  Non-TTY + multiple servers + no flag/env → error with the same
  "pick one with `--server`" candidate list.
- Unit tests only — table-driven coverage of every precedence rung and
  every error path. Slice 5 (`launch-default`) is the first real
  caller; this slice ships no command that exercises the resolver
  end-to-end.

## Capabilities

### New Capabilities
- `server-resolution`: a deterministic way for any pmox command to
  resolve the configured server it should target, given a flag, an
  env var, the config file, and a possibly-interactive terminal.

### Modified Capabilities
- `configure-and-credstore`: `cmd/pmox/configure.go` now imports the
  extracted `selectOne` helper from `internal/tui/picker` instead of
  defining it locally. No user-visible behavior change.
- `project-skeleton`: `rootCmd` gains a `--server` persistent flag.
  `configure` command documents that it ignores `--server` /
  `PMOX_SERVER`.

## Impact

- **New files**: `internal/server/resolver.go` + `resolver_test.go`,
  `internal/tui/picker.go` (extracted from `configure.go`).
- **Modified files**: `cmd/pmox/main.go` (add `--server` persistent
  flag), `cmd/pmox/configure.go` (import extracted picker).
- **No new dependencies**: `golang.org/x/term` already ships via
  slice 2.
- **No user-visible behavior change yet**: the resolver is dead code
  until slice 5 wires it into `launch`. Shipped now so slice 5 stays
  small.
- **Cross-slice contract**: this slice produces the
  `server.Resolve(ctx) (*Resolved, error)` surface that every command
  slice from 5 onward will consume. It reads the
  `*config.Config` shape produced by slice 2 and the
  `credstore.Get(url)` API from slice 2 — no changes required to
  either.
