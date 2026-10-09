## 1. Guard the linear path

- [x] 1.1 Add golden-output tests for the non-interactive `pmox init` flow (happy path, unreachable URL fail-fast, TLS fallback warning, changed-cert error, discovery fallbacks, snippet enable yes/no, cloud-init drift prompt) so later refactors are byte-checked
- [x] 1.2 Promote `bubbletea` and `bubbles` to direct dependencies in `go.mod`; add `github.com/charmbracelet/x/exp/teatest` for tests

## 2. Split deciding from printing (D5)

- [x] 2.1 Extract `classifyProbe` + `tlsFallbackNotices` returning `notice` values; make `probeURL` / `warnTLSFallback` thin printing wrappers
- [x] 2.2 Extract `checkPin` returning the pin and an optional `pinChange` (old/new fingerprints); keep `resolveInitPin` as the linear wrapper that asks via `confirmRepinFn`
- [x] 2.3 Extract discovery cores (`listNodeOptions`, template/storage/bridge option builders) returning options + notices + a manual-entry flag; keep the existing `pick*` functions as linear wrappers
- [x] 2.4 Extract `snippetPlan(pools)` from `offerEnableSnippets`; keep the linear `[Y/n]` prompt in the wrapper
- [x] 2.5 Extract `persistCore` returning `persistOutcome` (config path, cloud-init status incl. drift, template-build requested); keep `persistServer` printing and the drift prompt for the linear path
- [x] 2.6 In `internal/pvessh`, export `FetchHostKey(ctx, host)` (type + SHA256 fingerprint + key) and `AppendKnownHost(path, host, key)`; reimplement `PromptAndPinHostKey` on top of them; add unit tests
- [x] 2.7 Run the group-1 golden tests and confirm linear output is unchanged

## 3. Wizard shell (`internal/tui/wizard`)

- [x] 3.1 Define `Stage` interface and shell messages (`Next`, `GoTo`, `Busy`, `Idle`, `Error`, `Notice`, `Ask`, `Finish`, `Abort`)
- [x] 3.2 Implement `Model`: fixed header (`tui.Steps` + subtitle), active stage view, status line (spinner / error / notices), stable-height layout, `WindowSizeMsg` handling
- [x] 3.3 Implement modal `Dialog` (embedded huh confirm/select, bordered box, key capture, Esc = safe default)
- [x] 3.4 Implement key routing: Esc → back (or Review once reached), Ctrl-C → cancel root context + quit with `tui.ErrAborted`; Esc on the first stage shows a "Ctrl-C to quit" hint
- [x] 3.5 Add `Run(ctx, stages, opts) (Result, error)` that starts the inline program on stderr and returns the final result plus summary lines
- [x] 3.6 Add a `FormStage` helper that embeds a `*huh.Form` (overrides `SubmitCmd`/`CancelCmd`, forwards `WindowSizeMsg`, applies `tui.Theme()`)
- [x] 3.7 Unit tests for the shell: navigation, back semantics, dialog routing, spinner/error lines, abort result

## 4. Async operations (D4)

- [x] 4.1 Define `wizardOps` and the production implementation over `setup`, `pveclient` and `pvessh` (including concurrent `NodeResources`)
- [x] 4.2 Add request-id tagging so results from abandoned attempts are dropped
- [x] 4.3 Build a stub `wizardOps` for tests

## 5. pmox stages

- [x] 5.1 Connection stage: existing huh fields; on submit canonicalize → overwrite dialog → probe → pin check (re-pin dialog) → login/create token or use pasted token → verify; inline errors keep values; token-name collision message
- [x] 5.2 Defaults stage: node step (skipped for one node) → load node resources with spinner → single form (template incl. "build new" option, storage, snippet storage, bridge); fixed-value rendering for single options; manual-entry inputs + remediation notices on failures; enable-snippets dialog; "change node" action
- [x] 5.3 Access stage: SSH key choice with generate / existing + back, default user, node SSH user / auth / secret fields; host-key trust dialog before validation; inline SSH validation errors
- [x] 5.4 Review stage: masked rows + Confirm / Edit connection / Edit defaults / Edit access / Cancel; edit-from-Review returns to Review
- [x] 5.5 Persist stage: save with spinner via `persistCore`, cloud-init drift dialog, collect summary lines, finish with `buildTemplate` flag
- [x] 5.6 Seed stages for `config edit` (verified connection, current defaults/access, start on Review), replacing `runEditForm`'s loop

## 6. Wire up and remove the old loop

- [x] 6.1 Point `runInteractiveForm` and `runEditForm` at `wizard.Run`; print the summary after exit; run `offerBuiltTemplate` afterwards when requested; map abort / cancel / declined-overwrite to the existing errors and exit codes
- [x] 6.2 Delete `runFormLoop`, `printTabs`, `runReviewForm` and the per-stage seams (`establishConnectionFn`, `collectDefaultsFn`, `collectAccessFn`, `reviewFn`), migrating `init_form_test.go` to stage/shell tests
- [x] 6.3 Remove now-unused interactive-only helpers (`chooseSSHKeyAction` / `selectExistingKey` huh runs, interactive branch of `promptSSHAuthMethod`) if nothing else uses them

## 7. Verification and docs

- [x] 7.1 teatest end-to-end flows: happy path, unreachable-then-fixed URL, changed cert declined, first-time host key accepted, Esc back from Access, edit-from-Review, Ctrl-C mid-probe (exit 130, nothing written), config edit opens on Review
- [x] 7.2 Manual run against a real PVE cluster: fresh init, re-init over an existing server, config edit, build-template hand-off; check resize and narrow terminals
  - Done on PVE 9.1.1 through a pty + terminal emulator, in throwaway homes: fresh init (paste token, first-time host key, write), re-init over the same URL (overwrite declined → no changes; accepted → saved), config edit opens on Review, build-template hand-off reaches the image picker after save; 60x20 clips with the enlarge hint, wider than 100 columns keeps the 100-column frame. Found and fixed: the bootstrap SSH key was generated on Access submit, so quitting before Confirm left `~/.ssh/pmox_ed25519` behind; it is now created on save.
- [x] 7.3 `go vet`, `task lint`, `task test`, `openspec validate config-wizard-app`
- [x] 7.4 Update the README `pmox init` section and `llms.txt` to describe the single-screen wizard and its keys (Esc back, Ctrl-C quit)
