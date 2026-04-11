## Goals

1. A `pmox` binary exists, builds, lints, tests, and releases — even
   though it does nothing useful yet.
2. The shape of the repo is so close to tack's that someone fluent in
   tack can navigate pmox without reading docs.
3. No premature abstractions. No interfaces with one implementation.
   No packages added "for later."

## Non-goals

- Any subcommand beyond what `cobra.Command.Version` surfaces for free.
- Any PVE API client code, keychain integration, config file loading,
  cloud-init handling, SSH waiting, or hooks. Each of those is its own
  slice.
- Windows builds. Tack doesn't ship them; the parent design brief
  doesn't ask for them; go-keyring's Windows path isn't load-bearing
  until the credstore slice exists.
- A real `README.md`. It gets its own slice once there's something to
  document.

## Decisions

### D1. `cmd/pmox/main.go` is one file, not one-file-per-subcommand

The parent design brief lists `cmd/pmox/{configure,launch,list,info,
start,stop,clone,delete,version}.go` as separate files. Tack does the
opposite: `cmd/tack/main.go` is ~700 lines containing every Cobra
command inline, with `vault.go` and `export.go` as the only siblings
(both large enough to justify the split).

**Decision**: follow tack. Slice 1 ships `cmd/pmox/main.go` only. Future
slices may split out `configure.go` and `launch.go` because those two
commands are the gnarly ones (interactive prompting and the multi-step
state machine respectively). Trivial commands (`list`, `info`, `start`,
`stop`, `delete`, `clone`) stay inline.

**Why**: the parent design brief says "mirror tack's conventions
wherever you can" *and* lists a per-command file layout. These
contradict. Tack's choice has held up across ~10 commands and is
already familiar to anyone who knows the project — that's the
"instantly at home" goal we're optimizing for.

### D2. No `internal/log/` package

The parent brief lists `internal/log/`. Tack doesn't have one — verbose
and debug are bools threaded through executor structs and call sites
write to stderr via `fmt.Fprintln`. A logger package for one binary with
two verbosity levels is the kind of premature abstraction tack
deliberately avoids.

**Decision**: no log package. `cmd/pmox/main.go` declares
`var verbose bool; var debug bool` as persistent flag targets, and call
sites in `internal/...` that need conditional output take a `*log.Logger`
or `io.Writer` as a parameter from the caller. If this becomes painful
later, we add the package then.

### D3. `internal/exitcode/` ships in slice 1, not later

Tack doesn't have an exit-code package; it returns errors and lets
`os.Exit(1)` happen. pmox is more script-friendly than tack — `pmox` is
the kind of tool that gets called from CI pipelines that want to
distinguish "VM not found" (skip) from "auth failed" (alert) from
"network blip" (retry).

**Decision**: add `internal/exitcode/exitcode.go` in slice 1 with the
typed constants the parent brief asks for (`ExitOK`, `ExitUserError`,
`ExitAPIError`, `ExitNetworkError`, `ExitNotFound`, `ExitGeneric`) and
wire `main()` to translate top-level errors into the right code via a
small `errors.As` switch. Empty in slice 1; populated as later slices
introduce typed errors.

### D4. Workflow file extension is `.yaml`, not `.yml`

Tack uses `.yaml`. GitHub Actions accepts both. Match tack.

### D5. CI does not run an integration job in slice 1

Tack has an `integration` CI job that depends on `make test-integration`,
which spins up Docker SSH containers. pmox doesn't have integration
tests yet. Adding a no-op job now would just be noise.

**Decision**: ship `build`, `test`, `lint`, `validate` only. The
`validate` job in tack runs `make validate-examples`; pmox has no
examples yet, so `validate` is also dropped from slice 1 and will be
re-added by the slice that introduces `examples/`.

Final job set: `build`, `test`, `lint`. Three jobs.

### D6. `.goreleaser.yaml` mirrors tack byte-for-byte where possible

Same `version: 2` declaration. Same `before: hooks: [go mod tidy, go
generate ./...]`. Same archive name template. Same checksum block. Same
changelog grouping (Features / Bug Fixes / Other with the same regexes).
Same `release: mode: replace, prerelease: auto`.

**Diffs from tack's file**:
- `project_name: pmox`
- `binary: pmox`, `main: ./cmd/pmox`
- `release.github.owner: eugenetaranov`
- `release.github.name: pmox`
- `brews[0].repository.owner: eugenetaranov`
- `brews[0].description`: rewritten for pmox
- `brews[0].homepage: https://github.com/eugenetaranov/pmox`
- `brews[0].test`: `system "#{bin}/pmox", "--version"`
- Release header/footer prose updated for pmox

That's it. Everything else is identical.

### D7. Release workflow polls CI before publishing

Tack's `release.yaml` has a "Wait for CI to succeed" step that polls
`gh run list` for the same SHA before invoking goreleaser. This prevents
racing a tag push against a still-running CI run. Copy it verbatim,
swap the workflow name from `CI` to `CI` (still matches).

### D8. ldflag variables go in `cmd/pmox/main.go`, not a separate `internal/version/` package

Tack declares `var (version = "dev"; commit = "none"; date = "unknown")`
at package `main` and embeds them via `rootCmd.Version`. Same here.
A `version` package would be cleaner if multiple binaries shared it; we
have one binary.

## Risks

- **`HOMEBREW_TAP_TOKEN` not set on first release** → goreleaser fails
  the brew step but the GitHub release still publishes. Mitigation:
  call out the secret requirement in `tasks.md` and in the README's
  release section once the docs slice ships.
- **Go version drift** → if a contributor uses Go 1.23 locally, things
  might compile but lint differently. Mitigation: pin Go 1.24 in
  `go.mod` and in the CI matrix env var. No matrix across versions —
  tack doesn't either.
- **golangci-lint v1.64 may diverge from current upstream** → tack
  pinned this version explicitly, so do the same. Bumping it is its own
  small slice.
- **The Cobra dependency isn't actually exercised yet** → `pmox` with no
  subcommands prints usage and exits. That's fine; `make build` plus
  `./bin/pmox --version` plus `./bin/pmox` (which prints help) is enough
  to prove the wiring.
