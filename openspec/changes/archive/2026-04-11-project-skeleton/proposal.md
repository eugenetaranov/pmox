## Why

pmox is a greenfield repo. Before any subcommand can land, the binary
needs to exist, build reproducibly, embed version metadata, run in CI,
and cut releases via the same Homebrew-tap-publishing flow that tack
already uses. Doing this as a dedicated first slice keeps every later
change small: each subsequent proposal adds *behavior*, not scaffolding.

The shape of this slice is dictated almost entirely by what
[tackhq/tack](https://github.com/tackhq/tack) already does. Anyone who
knows tack's repo layout, Makefile targets, CI jobs, and goreleaser
config should feel instantly at home here. Where the parent design brief
and tack disagree, this proposal follows tack — see `design.md` for the
specific divergences and why.

## What Changes

- Add `go.mod` pinned to Go 1.24, with the minimum dependency set:
  `github.com/spf13/cobra`, `gopkg.in/yaml.v3`. Larger deps
  (`go-keyring`, `golang.org/x/crypto`) wait for slices that need them.
- Add `cmd/pmox/main.go` with the Cobra root command, persistent flags
  (`--debug`, `--verbose`, `--no-color`, `--output`), `version`/`commit`/`date`
  ldflag variables, and signal handling. No subcommands beyond the
  `version` info Cobra surfaces automatically via `rootCmd.Version`.
- Add `Makefile` with the target set tack ships: `build`, `build-all`,
  `build-linux`, `build-darwin`, `test`, `test-coverage`, `lint`, `clean`,
  `run`, `install`, `deps`, `release`, `release-dry-run`, `release-snapshot`,
  `release-check`. Same `LDFLAGS` shape, same `BUILD_DIR=bin`.
- Add `.goreleaser.yaml` mirroring tack's: linux+darwin × amd64+arm64,
  `tar.gz` archives, sha256 checksums, `replace`-mode GitHub release,
  Homebrew formula published to `eugenetaranov/homebrew-tap` via a
  `HOMEBREW_TAP_TOKEN` secret.
- Add `.github/workflows/ci.yaml` with `build`, `test`, `lint`, and
  `validate` jobs. Go 1.24, golangci-lint v1.64, `--timeout=3m`. Skip the
  `integration` job until a slice exists that needs it.
- Add `.github/workflows/release.yaml` triggered on `v*` tags. Polls the
  CI workflow for the same SHA via `gh run list` before invoking
  goreleaser, exactly like tack.
- Add `LICENSE` (MIT), `.gitignore` (Go + bin/ + coverage artifacts), and
  a placeholder `README.md` that says "see future slices" — the real
  README is its own slice (`docs-and-llms-txt`).
- Add an empty `internal/` directory marker (`.gitkeep`) so the layout is
  obvious to readers landing on the repo with nothing else committed yet.

## Capabilities

### New Capabilities
- `project-skeleton`: a buildable, releasable, lintable Go binary named
  `pmox` that prints version info and does nothing else useful yet.

### Modified Capabilities

_None — this is the first change in the repo._

## Impact

- **New files**: `go.mod`, `go.sum`, `cmd/pmox/main.go`, `Makefile`,
  `.goreleaser.yaml`, `.github/workflows/ci.yaml`,
  `.github/workflows/release.yaml`, `LICENSE`, `.gitignore`,
  `README.md`, `internal/.gitkeep`.
- **No code changes elsewhere**: the repo is empty before this slice.
- **Repo secrets required before first release**: `HOMEBREW_TAP_TOKEN`
  must be set in GitHub repo settings, scoped to write to
  `eugenetaranov/homebrew-tap`. Document this in `tasks.md` so it isn't
  forgotten on the first `git tag v0.1.0`.
- **No runtime behavior**: `pmox` exits cleanly with usage text, and
  `pmox --version` prints `dev (commit: none, built: unknown)` when built
  without ldflags, or the real values when built via `make build`.
