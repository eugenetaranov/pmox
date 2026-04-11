## D1. Resolver lives in `internal/server`, not `internal/config`

`internal/config` is a pure YAML loader — no network, no keychain, no
terminal I/O. The resolver pulls the secret from `credstore`, reads the
terminal, and prints picker UI, so it belongs in its own package.
`internal/server` is the obvious home and leaves room for future
server-scoped helpers (health checks, feature probing) without
bloating `config`.

## D2. `Resolved` bundles URL + Server + Secret

```go
type Resolved struct {
    URL    string          // canonical
    Server *config.Server  // the matched config block
    Secret string          // from credstore
}

func Resolve(ctx context.Context, opts Options) (*Resolved, error)
```

**Alternative considered:** return just the canonical URL, let each
command handler re-fetch the `*Server` and call `credstore.Get`.
Rejected — every future caller needs all three, and forcing them to
re-do the keychain dance at each call site is both duplication and a
correctness trap (what if they forget to wrap the error?).

**Consequence:** the resolver depends on `credstore`. Tests inject a
fake keychain via `keyring.MockInit()` (same pattern slice 2 already
uses). Not a real coupling concern — `credstore` is a leaf package.

**Error surface:** a keychain miss on an otherwise-resolved server is
an error from `Resolve`, not a partial success. Message:
`server https://host:8006/api2/json configured but secret not found in keychain; re-run 'pmox configure'`.

## D3. Precedence ladder — failures don't fall through

Each rung is either a resolution or a hard error. If `--server pve9`
is supplied and `pve9` doesn't match any configured server, we do
**not** fall through to "single configured server." The user asked for
`pve9` specifically, so the right behavior is an error.

```
┌─ rung 1: --server flag ──────────────────────────┐
│   set?  ──no──> rung 2                           │
│   set?  ──yes─> canonicalize + lookup            │
│                   hit ──> Resolved               │
│                   miss ─> error (candidates)     │
└──────────────────────────────────────────────────┘
┌─ rung 2: PMOX_SERVER env ────────────────────────┐
│   set?  ──no──> rung 3                           │
│   set?  ──yes─> canonicalize + lookup            │
│                   hit ──> Resolved               │
│                   miss ─> error (candidates)     │
└──────────────────────────────────────────────────┘
┌─ rung 3: single configured server ───────────────┐
│   count = 0 ──> error "run 'pmox configure'"     │
│   count = 1 ──> Resolved                         │
│   count > 1 ──> rung 4                           │
└──────────────────────────────────────────────────┘
┌─ rung 4: interactive picker ─────────────────────┐
│   TTY? ──no──> rung 5                            │
│   TTY? ──yes─> selectOne over ServerURLs()       │
│                ──> Resolved (or ctx.Err)         │
└──────────────────────────────────────────────────┘
┌─ rung 5: non-TTY ambiguity ──────────────────────┐
│   error "multiple servers configured; pick one   │
│   with --server or PMOX_SERVER" + candidates     │
└──────────────────────────────────────────────────┘
```

## D4. Matching semantics

Input forms the user might type:

| Input                                     | Treatment                                      |
|-------------------------------------------|------------------------------------------------|
| `https://pve1.lan:8006/api2/json`         | canonicalize (no-op), exact lookup             |
| `https://pve1.lan`                        | canonicalize adds `:8006/api2/json`, lookup    |
| `https://pve1.lan:8006`                   | canonicalize adds `/api2/json`, lookup         |
| `pve1.lan`                                | prepend `https://`, then canonicalize + lookup |
| `pve1.lan:8006`                           | prepend `https://`, then canonicalize + lookup |
| `pve1` (bare hostname, no dots)           | prepend `https://`, canonicalize, lookup       |

The "prepend `https://` if no scheme" step happens before
`config.CanonicalizeURL` sees the input. This is a small forgiving
layer so users don't have to type the scheme every time — slice 2's
`configure` is strict about https because it's the point of first
entry; a resolver flag is a different UX.

**Explicitly rejected:** hostname substring / prefix matching.
`--server pve` matching both `pve1.lan` and `pve2.lan` is an
ambiguity bug factory. Exact canonical match is predictable and tab-
completion friendly.

**On a miss**, the error lists the configured URLs verbatim so the
user can see the exact form they're being matched against.

## D5. Picker extraction scope

Move only the existing `selectOne(title string, opts []huh.Option[string], def string) string`
helper from `configure.go` to a new file `internal/tui/picker.go`.
Nothing else — no new API, no abstraction over the huh library, no
option builder DSL. The function stays byte-identical; `configure.go`
gains one import and loses the local definition.

**Why the extraction at all:** this is the second caller, which is
exactly when DRY stops being speculative. And the parked thread in
ROADMAP.md already flagged it.

**Why not extract `pickNode` / `pickTemplate` / etc. too:** those are
configure-specific auto-discovery flows that happen to use
`selectOne` internally. They don't generalize. The resolver's picker
is just `selectOne` over `cfg.ServerURLs()` — no discovery step.

## D6. `--server` as a persistent root flag

```go
rootCmd.PersistentFlags().String("server", "", "Proxmox server URL (overrides PMOX_SERVER)")
```

The flag lives on `rootCmd` so every subcommand gets it for free and
`--help` lists it once at the top level. `configure` silently ignores
it (the flag is still accepted — cobra doesn't let you per-command
hide a persistent flag cleanly, and "accepted but ignored" is the
least surprising behavior; we document the ignoring in the `configure`
help text).

Commands read the flag via `cmd.Flags().GetString("server")` and pass
it to `server.Resolve` as `Options.Flag`. The resolver reads
`os.Getenv("PMOX_SERVER")` itself — env lookup isn't a flag, so it
doesn't go through cobra.

## D7. TTY detection

Use `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))`. Already in the
dep tree from slice 2. The resolver takes a `Stdin` writer/fd in its
`Options` struct so tests can force non-TTY behavior without monkey-
patching globals.

```go
type Options struct {
    Cfg    *config.Config
    Flag   string        // value of --server, empty if unset
    Env    string        // value of PMOX_SERVER, empty if unset
    Stdin  *os.File       // for TTY detection + picker; os.Stdin in prod
    Stdout io.Writer      // picker draws here; os.Stdout in prod
    Stderr io.Writer      // error/prompt text; os.Stderr in prod
}
```

Passing `Env` in explicitly (instead of reading `os.Getenv` inside
`Resolve`) makes tests hermetic — no `t.Setenv` gymnastics. The
caller in `main.go` reads the env once and hands it over.

## D8. Error shapes

Errors returned from `Resolve` wrap `exitcode.ErrUserInput` (or a new
sentinel if the mapping table grows) so the existing `exitcode.From`
surface keeps working. Specifically:

- no servers configured → `exitcode.ErrNotFound`, message
  `"no server configured; run 'pmox configure' to add one"`
- flag/env miss → `exitcode.ErrUserInput`, message lists candidates
- non-TTY ambiguity → `exitcode.ErrUserInput`, message lists
  candidates and suggests `--server` or `PMOX_SERVER`
- keychain secret missing → `exitcode.ErrNotFound`, message
  `"secret for <url> not found in keychain; re-run 'pmox configure'"`
- picker cancelled (Ctrl+C) → plain `ctx.Err()`, already handled by
  the signal plumbing in `main.go`

## D9. Testing strategy

Unit tests only, covering:

1. **Precedence matrix** — table-driven, one row per combination of
   `(flag set?, env set?, #servers, is_tty?)` reaching each rung.
2. **Matching forms** — for each row in the D4 table, assert
   successful resolution against a config that contains the canonical
   form.
3. **Match failures** — flag/env pointing at a non-existent server,
   candidates list formatted correctly.
4. **Picker branch** — fake `Stdin` via a `*os.File` from `os.Pipe`
   that answers "1\n"; verify the first server is returned.
   (The picker already has a test scaffold in `configure_test.go` we
   can lift the pattern from.)
5. **Keychain miss** — use `keyring.MockInit()`, configure a server
   in memory without setting the secret, assert the specific error
   message.

No slice-5-style integration test until slice 5 exists. That's fine:
the resolver has no network I/O and no disk I/O of its own (the
config is passed in), so unit tests cover every branch.
