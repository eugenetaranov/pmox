## ADDED Requirements

### Requirement: Buildable Go binary

The repository SHALL contain a Go module that produces a single static
binary named `pmox` via `make build`, with no CGo dependencies and no
runtime requirements beyond a libc-compatible target OS.

#### Scenario: Local build on macOS or Linux
- **WHEN** a developer runs `make build` in a fresh clone with Go 1.24 installed
- **THEN** the build SHALL succeed and produce `bin/pmox`
- **AND** the binary SHALL execute and exit 0 when invoked with no arguments
- **AND** the binary SHALL print Cobra-generated usage text on stderr or stdout

#### Scenario: Cross-compilation
- **WHEN** a developer runs `make build-all`
- **THEN** the build SHALL produce `bin/pmox-linux-amd64`, `bin/pmox-linux-arm64`, `bin/pmox-darwin-amd64`, and `bin/pmox-darwin-arm64`

### Requirement: Version metadata embedded at build time

The binary SHALL embed version, commit, and build-date strings via
`-ldflags` so that release artifacts can be traced back to a specific
git revision.

#### Scenario: Version flag with ldflag injection
- **WHEN** the binary is built via `make build` from a tagged commit
- **AND** the user runs `pmox --version`
- **THEN** the output SHALL contain the tag, the short commit SHA, and an RFC3339 build date

#### Scenario: Version flag without ldflag injection
- **WHEN** the binary is built via `go build ./cmd/pmox` directly (no ldflags)
- **AND** the user runs `pmox --version`
- **THEN** the output SHALL be `pmox version dev (commit: none, built: unknown)`

#### Scenario: Cobra version subcommand
- **WHEN** the user runs `pmox version`
- **THEN** the output SHALL be identical to `pmox --version`

### Requirement: Persistent global flags

The root command SHALL register the persistent flags `--debug`/`-d`,
`--verbose`/`-v`, `--no-color`, and `--output`, matching the names,
shorthands, and defaults used by tack.

#### Scenario: Help text shows global flags
- **WHEN** the user runs `pmox --help`
- **THEN** the output SHALL list `--debug`, `--verbose`, `--no-color`, and `--output`
- **AND** the `--output` flag SHALL default to `text`

### Requirement: Signal handling

The binary SHALL install a signal handler that cancels in-flight work
on the first SIGINT or SIGTERM and force-exits with code 130 on the
second signal.

#### Scenario: First Ctrl-C cancels gracefully
- **WHEN** a long-running command is interrupted with a single SIGINT
- **THEN** the binary SHALL print `Interrupted, cleaning up...` to stderr
- **AND** SHALL allow deferred cleanup to run

#### Scenario: Second Ctrl-C force-exits
- **WHEN** a second SIGINT arrives before cleanup completes
- **THEN** the binary SHALL exit immediately with code 130

### Requirement: Typed exit codes

The binary SHALL exit with a distinct integer code for each broad
failure category (user error, not found, API error, network error,
unauthorized, generic), and a `pmox` package shall provide named
constants for each code.

#### Scenario: Successful run exits 0
- **WHEN** any command completes without error
- **THEN** the process SHALL exit with code 0

#### Scenario: Constants are exported from internal/exitcode
- **WHEN** a developer imports `github.com/eugenetaranov/pmox/internal/exitcode`
- **THEN** the package SHALL export `ExitOK`, `ExitGeneric`, `ExitUserError`, `ExitNotFound`, `ExitAPIError`, `ExitNetworkError`, and `ExitUnauthorized` as `int` constants

### Requirement: CI pipeline

The repository SHALL include a GitHub Actions workflow at
`.github/workflows/ci.yaml` named `CI` that runs on pushes to `main`,
tags matching `v*`, and pull requests to `main`.

#### Scenario: CI runs build, test, and lint jobs
- **WHEN** a pull request is opened against `main`
- **THEN** the workflow SHALL run three jobs: `build`, `test`, `lint`
- **AND** each job SHALL use Go 1.24
- **AND** the `lint` job SHALL run golangci-lint v1.64 with `--timeout=3m`

#### Scenario: CI build job verifies the binary
- **WHEN** the `build` job runs
- **THEN** it SHALL invoke `make build` and then `./bin/pmox --version`

### Requirement: Release pipeline

The repository SHALL include a GitHub Actions workflow at
`.github/workflows/release.yaml` named `Release` that runs goreleaser on
`v*` tag pushes after the matching CI run has succeeded.

#### Scenario: Release waits for CI before publishing
- **WHEN** a `v*` tag is pushed
- **THEN** the release workflow SHALL poll for the corresponding CI run
- **AND** SHALL only invoke goreleaser after the CI run reports `completed success`
- **AND** SHALL fail fast if the CI run reports any other completion status

#### Scenario: Release publishes Linux and macOS binaries
- **WHEN** goreleaser runs successfully against a `v*` tag
- **THEN** the GitHub release SHALL contain `pmox_<version>_linux_amd64.tar.gz`, `pmox_<version>_linux_arm64.tar.gz`, `pmox_<version>_darwin_amd64.tar.gz`, `pmox_<version>_darwin_arm64.tar.gz`, and `checksums.txt`

#### Scenario: Release publishes a Homebrew formula
- **WHEN** goreleaser runs successfully against a `v*` tag
- **AND** the `HOMEBREW_TAP_TOKEN` secret is configured
- **THEN** a commit SHALL be pushed to `eugenetaranov/homebrew-tap` adding or updating `Formula/pmox.rb`
- **AND** the formula SHALL include `system "#{bin}/pmox", "--version"` as its smoke test

### Requirement: Repository layout mirrors tack

The top-level repository structure SHALL mirror tack's so that anyone
familiar with tack can navigate pmox without documentation.

#### Scenario: Source layout
- **WHEN** a reader inspects the repo root
- **THEN** they SHALL find `cmd/pmox/`, `internal/`, `Makefile`, `.goreleaser.yaml`, `.github/workflows/`, `go.mod`, `LICENSE`, and `README.md`
- **AND** SHALL NOT find a `pkg/` directory
- **AND** SHALL NOT find any per-subcommand files in `cmd/pmox/` other than `main.go`

#### Scenario: Workflow file naming
- **WHEN** a reader inspects `.github/workflows/`
- **THEN** the files SHALL be named `ci.yaml` and `release.yaml` (with the `.yaml` extension, not `.yml`)
