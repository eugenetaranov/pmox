## 1. internal/tui/picker.go (extraction)

- [x] 1.1 Create `internal/tui/picker.go` with `func SelectOne(title string, opts []huh.Option[string], def string) string` — byte-identical copy of the `selectOne` body from `cmd/pmox/configure.go`
- [x] 1.2 Update `cmd/pmox/configure.go`: delete the local `selectOne`, add `"github.com/eugenetaranov/pmox/internal/tui"` import, rename call sites from `selectOne(...)` to `tui.SelectOne(...)`
- [x] 1.3 Run `go build ./...` and `go test ./...` — configure tests must still pass without modification

## 2. internal/server package skeleton

- [x] 2.1 Create `internal/server/resolver.go` with the `Options` struct from design D7 (`Cfg`, `Flag`, `Env`, `Stdin`, `Stdout`, `Stderr`)
- [x] 2.2 Declare `type Resolved struct { URL string; Server *config.Server; Secret string }`
- [x] 2.3 Declare `func Resolve(ctx context.Context, opts Options) (*Resolved, error)` stub that returns `errors.New("unimplemented")`
- [x] 2.4 Confirm `go build ./...` still passes

## 3. Matching helper

- [x] 3.1 Add unexported `func matchInput(input string, cfg *config.Config) (url string, server *config.Server, err error)` in `resolver.go`
- [x] 3.2 If `input` has no `://` prefix, prepend `https://` before calling `config.CanonicalizeURL`
- [x] 3.3 On canonicalize error, return an error wrapping `exitcode.ErrUserInput` with message `"invalid --server/PMOX_SERVER value %q: %w"`
- [x] 3.4 Exact-lookup the canonical form in `cfg.Servers`. On miss, return an error with message `"no configured server matches %q\nconfigured:\n  - <url1>\n  - <url2>"` (one per line, sorted via `cfg.ServerURLs()`)
- [x] 3.5 On hit, return the canonical URL and `*Server`

## 4. Precedence ladder

- [x] 4.1 In `Resolve`, rung 1: if `opts.Flag != ""`, call `matchInput` and hydrate `Resolved` (URL + Server + secret — see task 5)
- [x] 4.2 Rung 2: if `opts.Env != ""`, same as rung 1 but with `opts.Env`
- [x] 4.3 Rung 3: inspect `opts.Cfg.ServerURLs()`. If zero, return `fmt.Errorf("%w: no server configured; run 'pmox configure' to add one", exitcode.ErrNotFound)`. If one, hydrate `Resolved` for that URL
- [x] 4.4 Rung 4: if more than one server and `term.IsTerminal(int(opts.Stdin.Fd()))`, call `tui.SelectOne("Select server", opts, default)` over sorted `ServerURLs()`. Default selection: first entry
- [x] 4.5 Rung 5: if more than one server and not a TTY, return `fmt.Errorf("%w: multiple servers configured; pick one with --server or PMOX_SERVER\nconfigured:\n  - ...\n  - ...", exitcode.ErrUserInput)`
- [x] 4.6 After any successful rung, verify `ctx.Err() == nil` before returning

## 5. Secret hydration

- [x] 5.1 Add unexported `func hydrate(url string, server *config.Server) (*Resolved, error)` that calls `credstore.Get(url)` and builds `*Resolved`
- [x] 5.2 On `credstore.ErrNotFound`, return `fmt.Errorf("%w: secret for %s not found in keychain; re-run 'pmox configure'", exitcode.ErrNotFound, url)`
- [x] 5.3 On any other credstore error, wrap with `fmt.Errorf("load secret for %s: %w", url, err)`

## 6. --server flag wiring

- [x] 6.1 In `cmd/pmox/main.go` `init()`, add `rootCmd.PersistentFlags().String("server", "", "Proxmox server URL (overrides PMOX_SERVER)")`
- [x] 6.2 Add a one-line comment above the flag noting that `configure` ignores both the flag and `PMOX_SERVER`
- [x] 6.3 Verify `pmox --help` still renders cleanly and shows `--server` in the global flags section
- [x] 6.4 Verify `pmox configure --help` also shows `--server` (cobra has no per-command hide for persistent flags; that's expected)

## 7. Exit code wiring

- [x] 7.1 Confirm `internal/exitcode/exitcode.From` already maps `ErrUserInput` and `ErrNotFound` correctly (slice 2 added these). If not, extend
- [x] 7.2 Add one `exitcode_test.go` case that wraps each new resolver error shape through `errors.Is` and asserts the expected exit code

## 8. Unit tests

- [x] 8.1 Create `internal/server/resolver_test.go` with a helper that builds an `Options` struct pointed at an in-memory `*config.Config` with N servers and a piped stdin
- [x] 8.2 Table-driven **precedence matrix** test: columns `(flagSet, envSet, numServers, isTTY)`, one row per reachable combination, asserting which rung fires. Use a fake stdin `*os.File` via `os.Pipe()` to simulate TTY / non-TTY
- [x] 8.3 Table-driven **matching forms** test from design D4: each input form (`pve1`, `pve1.lan`, `pve1.lan:8006`, `https://pve1.lan`, full canonical) resolves successfully against a config containing the canonical form
- [x] 8.4 **Flag/env miss** test: config has `pve1`, flag is `pve9`, assert error message contains `"no configured server matches"` and lists `pve1`
- [x] 8.5 **Zero servers** test: empty config, no flag/env, assert `ErrNotFound` with `"run 'pmox configure'"` text
- [x] 8.6 **Non-TTY ambiguity** test: two servers, no flag, no env, non-TTY stdin, assert `ErrUserInput` listing both candidates
- [x] 8.7 **Picker branch** test: two servers, no flag, no env, piped stdin delivering the right keystrokes to select the first entry. Reuse the fake-terminal pattern from `cmd/pmox/configure_test.go`
- [x] 8.8 **Keychain miss** test: use `keyring.MockInit()` (no prior `Set`), assert `ErrNotFound` with `"re-run 'pmox configure'"` text
- [x] 8.9 **Context cancellation** test: cancel the context before `Resolve`, assert `ctx.Err()` is returned (not swallowed)
- [x] 8.10 Run `go test ./internal/server/... -race` — all green

## 9. Documentation

- [x] 9.1 No README update this slice — placeholder README from slice 2 still says "currently the only working subcommand is `pmox configure`," which remains true since the resolver has no user-facing entry point yet
- [x] 9.2 Add a one-paragraph godoc comment at the top of `internal/server/resolver.go` describing the precedence ladder (this is the reference future commands will read)

## 10. Smoke test

- [x] 10.1 `go build ./...` passes
- [x] 10.2 `go test ./... -race` passes
- [x] 10.3 `golangci-lint run` (via `make lint`) passes
- [x] 10.4 `pmox --help` shows `--server` in the flag list
- [x] 10.5 `pmox --server https://nope.lan list` is not yet meaningful (no `list` command), so this slice has no end-to-end smoke. Slice 5 will be the first real exercise.
