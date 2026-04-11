## 1. Module bootstrap

- [x] 1.1 Initialize `go.mod` with `module github.com/eugenetaranov/pmox` and `go 1.24`
- [x] 1.2 `go get github.com/spf13/cobra@v1.8.0`
- [x] 1.3 `go get gopkg.in/yaml.v3` (used for config in a later slice; pull it in now so the dep set is stable from day one)
- [x] 1.4 `go mod tidy` and commit `go.sum`

## 2. Binary entrypoint

- [x] 2.1 Create `cmd/pmox/main.go` with package `main` and the doc comment `// Package main is the entrypoint for the pmox CLI.`
- [x] 2.2 Declare `var (version = "dev"; commit = "none"; date = "unknown")` at package scope for ldflag injection
- [x] 2.3 Declare persistent flag targets: `var (debug bool; verbose bool; noColor bool; outputMode string)`
- [x] 2.4 Define `rootCmd` with `Use: "pmox"`, `Short`, `Long`, and `Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)`
- [x] 2.5 Wire persistent flags in `init()`: `--debug`/`-d`, `--verbose`/`-v`, `--no-color`, `--output` (default `"text"`)
- [x] 2.6 Implement `signalContext(parent)` mirroring tack's pattern: cancel on first SIGINT/SIGTERM, force-exit 130 on second signal
- [x] 2.7 `main()` calls `rootCmd.Execute()` and exits 1 on error
- [x] 2.8 Verify: `go run ./cmd/pmox` prints help and exits 0; `go run ./cmd/pmox --version` prints `pmox version dev (commit: none, built: unknown)`

## 3. Internal package stubs

- [x] 3.1 Create `internal/exitcode/exitcode.go` with constants `ExitOK = 0`, `ExitGeneric = 1`, `ExitUserError = 2`, `ExitNotFound = 3`, `ExitAPIError = 4`, `ExitNetworkError = 5`, `ExitUnauthorized = 6`
- [x] 3.2 Add `func From(err error) int` returning `ExitOK` for nil and `ExitGeneric` for non-nil; later slices extend it via `errors.As`
- [x] 3.3 Wire `main()` to call `os.Exit(exitcode.From(rootCmd.Execute()))` instead of the bare `os.Exit(1)`
- [ ] 3.4 Add a placeholder `internal/.gitkeep` so the empty `internal/` tree shows up in fresh clones (delete once a real package lives there)

## 4. Makefile

- [x] 4.1 Create `Makefile` with `BINARY=pmox`, `BUILD_DIR=bin`, and the same `VERSION/COMMIT/DATE` git-derived defaults tack uses
- [x] 4.2 Targets: `list`, `build`, `build-all`, `build-linux`, `build-darwin`, `test`, `test-coverage`, `lint`, `clean`, `run`, `install`, `deps`, `release`, `release-dry-run`, `release-snapshot`, `release-check`
- [x] 4.3 `build` produces `bin/pmox` with `-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"`
- [x] 4.4 `release` target replicates tack's interactive tag bumper (latest tag → suggested next patch → prompt → tag → push)
- [x] 4.5 Verify: `make build && ./bin/pmox --version` prints real values from `git describe`

## 5. GoReleaser

- [x] 5.1 Create `.goreleaser.yaml` with `version: 2`, `project_name: pmox`
- [x] 5.2 `before.hooks: [go mod tidy, go generate ./...]`
- [x] 5.3 `builds[0]`: id `pmox`, binary `pmox`, main `./cmd/pmox`, `CGO_ENABLED=0`, goos `[linux, darwin]`, goarch `[amd64, arm64]`, ldflags identical to tack's
- [x] 5.4 `archives[0]`: format `tar.gz`, name template identical to tack, files `[README.md, LICENSE*]` (no `docs/*` until docs exist)
- [x] 5.5 `checksum`, `snapshot`, `changelog` blocks copied from tack (changelog regexes unchanged)
- [x] 5.6 `release`: owner `eugenetaranov`, name `pmox`, mode `replace`, prerelease `auto`, header/footer prose rewritten for pmox
- [x] 5.7 `brews[0]`: name `pmox`, repository owner `eugenetaranov`, name `homebrew-tap`, `directory: Formula`, `token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"`, MIT license, test `system "#{bin}/pmox", "--version"`, install `bin.install "pmox"`
- [x] 5.8 `announce.skip: true`
- [x] 5.9 Verify: `make release-check` passes; `make release-dry-run` produces artifacts under `dist/` for all four target triples

## 6. CI workflow

- [x] 6.1 Create `.github/workflows/ci.yaml` named `CI`
- [x] 6.2 Triggers: `push` to `main` and tags `v*`, `pull_request` to `main`
- [x] 6.3 `env.GO_VERSION: '1.24'`
- [x] 6.4 Job `build`: checkout, setup-go, `make build`, verify with `./bin/pmox --version`
- [x] 6.5 Job `test`: checkout, setup-go, `go test -v -short -race -coverprofile=coverage.out ./...`, upload coverage with `continue-on-error: true`
- [x] 6.6 Job `lint`: checkout, setup-go, `golangci-lint-action@v6` with `version: v1.64` and `args: --timeout=3m`
- [x] 6.7 Drop `validate` and `integration` jobs — neither has anything to run yet (see design D5)

## 7. Release workflow

- [x] 7.1 Create `.github/workflows/release.yaml` named `Release`
- [x] 7.2 Trigger: `push.tags: ['v*']`
- [x] 7.3 `permissions: contents: write`
- [x] 7.4 Step "Wait for CI to succeed": copy tack's `gh run list` polling loop verbatim
- [x] 7.5 Steps: checkout (`fetch-depth: 0`), setup-go 1.24 (note: tack pins 1.21 here for goreleaser; we use 1.24 to match the CI build)
- [x] 7.6 `goreleaser/goreleaser-action@v6` with `version: '~> v2'`, `args: release --clean`
- [x] 7.7 Env: `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`, `HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}`
- [x] 7.8 Document the `HOMEBREW_TAP_TOKEN` secret requirement in this tasks file (DONE — see 9.1) and in README once the docs slice ships

## 8. golangci-lint config

- [x] 8.1 Create `.golangci.yaml` matching tack's enabled linter set (TODO: fetch tack's `.golangci.yaml` when this slice is implemented; placeholder default config until then)
- [x] 8.2 Verify: `golangci-lint run --timeout=3m` passes against the empty skeleton

## 9. Repo housekeeping

- [x] 9.1 Repo-secrets prerequisite: `HOMEBREW_TAP_TOKEN` must be set in GitHub repo Settings → Secrets and Variables → Actions before tagging the first release. Without it, the goreleaser brew step fails but the GitHub release still publishes.
- [x] 9.2 Add `LICENSE` (MIT, copyright `2026 Eugene Taranov`)
- [x] 9.3 Add `.gitignore` (Go template + `bin/`, `dist/`, `coverage.out`, `coverage.html`, `.envrc`)
- [x] 9.4 Add a placeholder `README.md` with one paragraph: "pmox is a multipass-style CLI for Proxmox VE. This repo is under construction; see openspec/changes/ for in-flight work." Real README ships in `docs-and-llms-txt`.

## 10. Smoke tests

- [x] 10.1 `make build` succeeds on macOS and Linux
- [x] 10.2 `./bin/pmox` prints help and exits 0
- [x] 10.3 `./bin/pmox --version` prints version with real git-derived values
- [x] 10.4 `./bin/pmox version` (cobra-autogenerated) prints the same string
- [x] 10.5 `make test` passes (no tests yet → exits 0)
- [x] 10.6 `make lint` passes
- [x] 10.7 `make release-dry-run` produces `dist/pmox_*_{linux,darwin}_{amd64,arm64}.tar.gz` and a `checksums.txt`
- [ ] 10.8 Push a throwaway `v0.0.1-test1` tag in a fork (or use `act`) to verify the release workflow end-to-end before cutting the real `v0.1.0`
