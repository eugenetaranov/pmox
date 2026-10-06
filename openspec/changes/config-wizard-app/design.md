## Context

Today's interactive `pmox init` / `pmox config edit` (`runFormLoop` in
`cmd/pmox/init_form.go`) is a `for { switch stage }` loop. Each stage
runs one or more independent huh programs: `huh.Form.Run()`,
`tui.SelectOne`, `tui.Confirm`, each of which starts and tears down its
own bubbletea program. In between, `prompter.Printf`/`Errf` write plain
lines, and some steps still read raw `[y/N]` text with `prompter.Prompt`:

| Stage      | Today's mechanics |
|------------|-------------------|
| Connection | huh form → `CanonicalizeURL` → overwrite `tui.Confirm` → `probeURL` (prints) → `resolveInitPin` (may print + `tui.Confirm`) → login/token create (prints) → `validateCredentials` (prints TLS warning) → loop back to the form on failure |
| Defaults   | five sequential pickers, each doing its own 5 s API call; auto-select prints `Title: value`; failures fall back to `p.Prompt("Default node: ")`; zero snippet storages → raw `[Y/n]` prompt + `UpdateStorageContent` |
| Access     | `chooseSSHKeyAction` / `selectExistingKey` huh selects with a back loop → `promptDefaultUser` (raw text) → `promptNodeSSH`: host-key pin via `pvessh.PromptAndPinHostKey(w, r)` (raw I/O), raw user prompt, auth select, raw password/key prompts, "Verifying… ok" |
| Review     | rows printed with `Printf`, then a `tui.Select` |
| Persist    | `setup.SaveServer`, prints, cloud-init drift raw `[y/N]` prompt, optional `offerBuiltTemplate` (long streaming build) |

The linear (non-TTY) path reuses many of the same helpers. It must keep
its exact output, because scripts and tests depend on it.

huh v1 `*huh.Form` is a `tea.Model`. It exposes `SubmitCmd` / `CancelCmd`
(set to `tea.Quit` / `tea.Interrupt` only by `Form.Run`), so it can be
embedded as a child model in a bigger program.

## Goals / Non-Goals

**Goals:**
- One `tea.Program` for the whole interactive init / config-edit
  session. The header, page, status line and dialogs are rendered in
  place, with no inter-page scrollback.
- No blocking I/O inside `Update`. Every network/SSH/disk side effect is
  a `tea.Cmd` that returns a typed message.
- Reuse huh fields, validators and `tui.Theme()` unchanged.
- A generic wizard shell that later slices (`--regen-cloud-init`,
  `create-template`, `cleanup`) can reuse.
- Keep the linear path's behavior and output identical.

**Non-Goals:**
- Migrating the standalone pickers (`delete`, `stop`, `clone`,
  `use-context`, …) or `create-template`'s build UI.
- Dropping huh. That was considered and rejected; see D2.
- Changing what gets configured, validated or persisted.

## Decisions

### D1. Full-screen frame on stderr; summary printed after exit

The program runs with `tea.WithOutput(os.Stderr)` and
`tea.WithAltScreen()`. The view is a single-column card:

- a rounded frame titled `pmox init` (or `pmox config edit · <context>`);
- tabs with ✓ for stages already passed and ● for the active one, plus
  the stage subtitle;
- the page body, with a spinner, error or dialog under it;
- a footer with the keys.

Stages and huh forms are sized to the frame's inner width (at most 100
columns). Long lines wrap and are never truncated. In full-screen mode
the body stretches so the footer sits at the bottom of the terminal.

- *Why full-screen:* the first inline version looked almost the same as
  the old chain of forms. The user chose a full-screen card so the
  wizard clearly reads as one app.
- *Summary:* the alternate screen is discarded on exit, so `wizard.Run`
  returns `Result.Summary`. `runWizard` prints it after the terminal is
  restored: info lines to stdout, warnings to stderr. It is then
  followed by any template build.
- *Why stderr:* `tui.Interactive()` already requires a stderr TTY, and
  this keeps stdout clean.
- Inline rendering is still available through `Options.AltScreen=false`.

### D2. Embed huh forms, don't re-implement fields

Each page body is a freshly built `*huh.Form` with
`SubmitCmd = func() tea.Msg { return formSubmittedMsg{} }` and
`CancelCmd = nil` (the shell owns Ctrl-C/Esc). The stage forwards
messages to the form until `form.State == huh.StateCompleted`, then
reads the bound values.

- *Why:* zero rework of inputs, selects, password echo, validation,
  hide-funcs, theme, or help keys.
- *Alternative:* hand-built `bubbles/textinput` + `list`. That means
  much more code and test surface for the same UX. Rejected by the user
  in favor of keeping huh.

### D3. Generic shell in `internal/tui/wizard`, pmox stages in `cmd/pmox`

```
internal/tui/wizard            cmd/pmox
┌─────────────────────────┐    ┌────────────────────────────────┐
│ Model                    │    │ connectionStage  (huh form)    │
│  header: tui.Steps+sub   │◀──▶│ defaultsStage    (node → form) │
│  stages []Stage          │    │ accessStage      (key → form)  │
│  status: spinner|err|note│    │ reviewStage      (rows+select) │
│  dialog: *Dialog (modal) │    │ persistStage     (spinner)     │
│  result  []string        │    │ wizardOps        (async I/O)   │
└─────────────────────────┘    └────────────────────────────────┘
```

`Stage` interface: `ID() string`, `Title()/Subtitle()`, `Enter(ctx)
tea.Cmd`, `Update(tea.Msg) (Stage, tea.Cmd)`, `View() string`.
Stages talk to the shell only through messages: `wizard.Next`,
`wizard.GoTo(id)`, `wizard.Busy(label)`, `wizard.Idle`,
`wizard.Error(err)`, `wizard.Notice(text)`, `wizard.Ask(Dialog)`,
`wizard.Finish(result)`, `wizard.Abort`. The shell owns the spinner,
the status line, the modal dialog, key routing (a dialog captures input
while it is open) and quitting.

- *Why split:* the shell is pmox-agnostic and unit-testable on its own.
  The stages stay next to the existing init code and its unexported
  helpers.

### D4. Side effects behind a `wizardOps` interface

```go
type wizardOps interface {
    Probe(ctx, canonical) setup.ProbeResult
    ResolvePin(ctx, cfg, canonical, insecure, accepted) (pinCheck, error) // returns "changed" without prompting
    Login(ctx, url, insecure, pin, user, pw) (tokenIssuer, error)
    VerifyToken(ctx, url, id, secret, knownInsecure, pin) (bool, error)
    ListNodes(ctx, client) ([]pveclient.Node, error)
    NodeResources(ctx, client, node) (nodeResources, error) // templates+storage+bridges, fetched concurrently
    EnableSnippets(ctx, client, storage, content) error
    FetchHostKey(ctx, host) (hostKey, error)
    PinHostKey(knownHosts, host, hostKey) error
    ValidateSSH(ctx, pvessh.Config) error
    Persist(ctx, cfg, persistInput) (persistOutcome, error)
}
```

Each call is wrapped in a `tea.Cmd` that carries the stage's request
id, so a stale result from an abandoned attempt (the user went back
mid-probe) is ignored. Production wires the existing
`setup`/`pveclient`/`pvessh` functions; tests use a stub.

### D5. Separate deciding from printing (shared with the linear path)

Helpers that currently decide *and* print are split into a pure core
that returns `notice` values (level + text) and a thin linear wrapper
that prints them exactly as today:

- `probeURL` → `classifyProbe(r) (insecure, ok bool, notices []notice)`
- `warnTLSFallback` → `tlsFallbackNotices(url)`
- `resolveInitPin` → `checkPin(...) (pin string, changed *pinChange, err)`. The linear path asks with `confirmRepinFn`; the app opens a dialog.
- discovery fallbacks (`could not list …` + remediation) → notices + `needsManualEntry` flag
- `offerEnableSnippets` → `snippetPlan(pools) (target, ok)` + caller-side confirm
- `persistServer` → `persistCore(...) persistOutcome` (config path, cloud-init status incl. `drift`, template-build requested); printing and the drift prompt stay in the linear wrapper
- `pvessh.PromptAndPinHostKey` → exported `FetchHostKey` + `AppendKnownHost` (the existing function becomes a wrapper over them)

Linear-path golden tests guard the "identical output" goal.

### D6. Defaults page: one form, node change reloads

Nodes are listed first. A single node is shown as a fixed value. When
there are several, the node is a select on the same page as template,
storage, snippet storage and bridge. That node's templates, storages
and bridges load concurrently behind one spinner (`NodeResources`). If
the page is submitted with a different node than the one loaded, its
resources are reloaded and the page is shown again rather than
advancing. Single-option fields render as fixed notes. Empty or failed
lists become text inputs, with the remediation notice shown above the
form. If the node list itself fails, a node-only input is shown first.

- *Why not huh `OptionsFunc` keyed on node:* huh evaluates it
  synchronously inside `Update`, so the 5 s API calls would freeze the
  UI.
- *Why not a separate node step (the first draft):* a single page with
  reload-on-change needs one fewer screen and gives the same result.

### D7. Dialogs are modal huh confirms/selects owned by the shell

`wizard.Ask(Dialog{Title, Body, Field, OnResult})` renders a bordered
box under the page and routes keys to it. Used for: overwrite existing
server, changed TLS cert (both fingerprints shown, default **No**),
first-time SSH host key (type + SHA256 fingerprint, default No), enable
snippets on `<storage>` (default Yes, as today), regenerate drifted
cloud-init (default No).

### D8. Keys, back and abort

- Within a form: huh defaults (Tab/Shift-Tab, Enter).
- `Esc` on a page: back to the previous stage. Once Review has been
  reached, it returns to Review, keeping the current `reachedReview`
  semantics. `Esc` in a dialog picks the dialog's safe default.
- `Ctrl-C` anywhere: cancel the root context (in-flight probes abort),
  quit, and return `tui.ErrAborted` (exit 130). Nothing is written
  unless Persist already completed.

### D9. Template build stays outside the app

When the template choice is the `createTemplateSentinel`, the Persist
stage saves with an empty template (as today) and finishes with
`result.buildTemplate = true`. After `Program.Run` returns,
`runInteractiveForm` calls the existing `offerBuiltTemplate` with
normal streaming output. Embedding a multi-minute build log is
deferred to the `create-template` slice.

### D10. Testing

- Shell: table tests that feed messages to `wizard.Model` and assert on
  `View()` and the emitted messages, with no TTY.
- Stages: drive `Update` with synthetic `formSubmittedMsg` / op-result
  messages and a stub `wizardOps`. This replaces today's
  `establishConnectionFn` / `collectDefaultsFn` / `reviewFn` seams.
- A few end-to-end flows with `teatest` (fixed terminal size, golden
  final output): happy path, unreachable-then-fixed URL, changed cert
  declined, Esc-back from Access to Defaults, Ctrl-C mid-probe.

## Risks / Trade-offs

- [huh forms behave differently when embedded (focus, `WindowSizeMsg`, quitting on submit)] → always override `SubmitCmd`/`CancelCmd`; forward `WindowSizeMsg` and set `WithWidth`; cover with teatest flows.
- [Inline rendering glitches when the page shrinks or on resize] → keep the view height stable (reserve status/dialog lines); handle `WindowSizeMsg`; fall back to the alt screen behind an option if needed.
- [Pure/printing split changes linear output by accident] → add golden tests for the linear path before refactoring (first task group).
- [Stale async results after back-navigation] → request ids on every op message (D4).
- [Large single change to the most-used setup path] → land in task-group order behind passing tests. The old `runFormLoop` stays until the app reaches parity, then is deleted in one commit, so a revert is clean.

## Migration Plan

No config or on-disk format changes. Ship it as a normal release. The
rollback is a revert of the slice; config files written by either
version are identical.

## Open Questions

- Should `Esc` on the Connection stage (no previous stage) abort, or do
  nothing? Proposed: do nothing, and show a hint "Ctrl-C to quit".
- Should `init --regen-cloud-init` move onto the shell in this slice
  (it reuses the Access key picker)? Proposed: next slice.
