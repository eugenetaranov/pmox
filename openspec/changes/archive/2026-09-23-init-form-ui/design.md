## Context

`runInteractive` (`cmd/pmox/init.go`) runs a linear sequence:
`promptReachableURL` → overwrite check → `acquireToken` (paste/generate) →
`validateCredentials` → discovery pickers (`pickNode`/`pickTemplate`/
`pickStorage`/`pickSnippetStorage`/`pickBridge`) → `promptSSHKey` → default
user → `promptNodeSSH` → save. Each step is its own prompt; there is no
look-ahead, no going back, and no pre-write review.

The flow is data-dependent: discovery pickers need a working client
(URL+token), so questions can't all be shown up front.

## Goals / Non-Goals

**Goals:**
- Group prompts into navigable form pages (see-ahead, edit-in-place).
- A final review that can jump back and edit before writing anything.
- Preserve the discovery-driven flow and all existing validation.
- Keep the logic unit-testable despite the TUI.

**Non-Goals:**
- One flat form with static node/template entry (drops discovery).
- Changing what gets written to config.yaml, or the secret handling.
- Reworking `--list`/`--remove`/`--regen-cloud-init`.

## Decisions

### 1. Orchestration phases
Rewrite `runInteractive` as:
1. `collectConnection(defaults)` → Connection form (URL, token source +
   fields).
2. probe reachability (reuse `probeURL`) + `validateCredentials` /
   `generateToken`; on failure return to the Connection form.
3. overwrite check; build client; discover node/template/storage/bridge.
4. `collectDefaults(discovered)` → Defaults form (selects pre-filled).
5. `collectAccess(defaults)` → Access form (ssh key, user, node SSH),
   validating node SSH via the existing handshake.
6. `reviewAndConfirm(answers)` → review screen; Confirm writes, Back edits.

### 2. Forms via huh, behind seams
Each `collectX` is a package-level function variable returning a typed
struct (e.g. `connectionAnswers`, `defaultsAnswers`, `accessAnswers`):
```go
var collectConnectionFn = runConnectionForm // huh implementation
```
Production builds `huh.NewForm(huh.NewGroup(...))` with:
- an `Input` for the URL,
- a `Select` for the token source,
- conditional `Group`s (`WithHideFunc`) for the generate vs paste fields.
Tests stub `collectConnectionFn` to return canned answers, so orchestration
is testable without a TTY. The underlying logic (`CanonicalizeURL`,
`pveclient.Probe`, `CreateToken`, `sshkey.Generate`, …) keeps its existing
direct unit tests.

### 3. In-page validation
URL and credentials are validated as the Connection page submits. Prefer
huh field `Validate` funcs where cheap; network probe + auth may run after
the form returns, looping back to a fresh form pre-filled with the prior
answers on failure (so the user fixes rather than restarts). The probe's
TLS-insecure decision is captured and reused by `validateCredentials`
(as today).

### 4. Discovery → Defaults form
After auth, run discovery once and pass the option lists into the Defaults
form as `Select` options. Single-option lists are pre-selected (and may be
shown read-only or auto-filled, preserving the current auto-select
behavior). Template options depend on the chosen node; v1 may discover for
the resolved/default node and offer a node `Select`, re-querying templates
if the node changes (huh `OptionsFunc`) — or, if that proves fiddly, keep
node selection immediately before the Defaults form. Either way the
node/template/storage/bridge values are reviewable together.

### 5. Review screen
`reviewAndConfirm` renders every value (secrets masked) and offers
Confirm / Back-to-<page>. Choosing a page re-runs that `collectX` form
pre-filled with current answers; editing Connection re-runs probe/auth and
re-discovery (later answers may change). Only Confirm calls the existing
save path (config write + `credstore.Set`).

### 6. Non-interactive fallback
Forms need a TTY. When `interactiveFn()` is false, fall back to the current
text-prompt collectors (already present) or error with guidance, so
`--no-input`/pipes/CI behave exactly as before.

## Risks / Trade-offs

- **Test coverage of the most-tested command.** Mitigation: the UI/logic
  split — orchestration tested via stubbed form seams; logic keeps its
  direct tests. Do not inline network/logic into huh callbacks.
- **Back-from-Defaults invalidates discovery.** Editing Connection must
  re-auth and re-discover; the review flow handles this by re-running from
  the edited phase forward rather than patching in place.
- **huh conditional-group ergonomics** (generate vs paste, node→template)
  can get intricate. Mitigation: start with separate groups + `WithHideFunc`;
  fall back to a plain select-then-page split if a single form is awkward.
- **Scope creep.** Keep parity with today's behavior first (same values
  written, same validation); the form is a presentation change, not new
  configuration surface.
