## Why

`pmox init` and `pmox config edit` look like a wizard, but they are really
a chain of separate huh programs started one after another, with plain
`Printf`/`Errf` lines and raw `[y/N]` text prompts printed between them.
Every page, picker, probe, spinner, warning and confirmation adds more
scrollback. Errors scroll away with the page they belong to. The tab
bar is printed again, not updated in place. Some steps (the host-key
pin, enabling snippets, regenerating cloud-init) switch back to
line-mode prompts in the middle of the form. The result looks stitched
together, and each new step adds another seam. Running the whole
configuration flow as one persistent bubbletea program fixes this. It
also gives later commands a base to migrate onto.

## What Changes

- `pmox init` (interactive) and `pmox config edit` run as **one
  bubbletea program** for their whole lifetime. A fixed header (tab bar
  + subtitle) sits above a single page area that is redrawn in place,
  with an inline status/error line under it.
- Each page body is a huh form **embedded as a component** (huh forms
  are bubbletea models). Existing fields, validation and theme are
  reused instead of rewritten.
- Network and SSH work (reachability probe, login + token creation,
  credential check, cert re-pin check, node/template/storage/bridge
  discovery, SSH host-key fetch, SSH validation) runs as **async
  commands with an inline spinner**. A failure shows on the page it
  belongs to, with the user's answers preserved, instead of being
  printed above the next form.
- Mid-flow decisions become **in-app dialogs** instead of raw text
  prompts: overwrite an existing server, trust a changed TLS
  certificate, pin the node's SSH host key, enable `snippets` on a
  storage, regenerate a drifted cloud-init file.
- The Defaults stage presents all discovered values (node, template,
  storage, snippet storage, bridge) on **one page**. Template, storage
  and bridge options reload when the node changes. A single-option
  field is shown as a fixed value, not a picker.
- The Access stage holds the SSH-key choice (generate / pick existing,
  with back navigation), the default user, and the node SSH login
  (user, auth method, password or key + passphrase) on one page.
- Review and persistence happen inside the app. Saving shows a spinner,
  and the outcome lines (config path, cloud-init path, any warnings)
  stay on screen as a result summary when the program exits.
- "Build a new template now" still **hands off after the app exits**.
  The minutes-long `create-template` build keeps its existing
  streaming output.
- The non-interactive linear path (`--no-input`, no TTY,
  `--output json`) is **unchanged**, byte for byte.
- Shared logic currently coupled to the `prompter` (probe
  classification, TLS-fallback warning text, discovery listing,
  snippet-enable, host-key pinning) is split into pure functions that
  return results and messages. The linear flow prints them; the app
  renders them.

Out of scope: `init --regen-cloud-init`, `config use-context`, and the
picker-only commands (`delete`, `stop`, `clone`, …) keep their current
huh pickers. They are candidates for follow-up slices on the same app
shell.

## Capabilities

### New Capabilities

- `config-wizard-app`: the interactive configuration experience for
  `pmox init` and `pmox config edit` as a single persistent terminal
  app. It covers the stage layout, in-place rendering, async
  validation with inline errors, in-app dialogs, review/persist, the
  template-build hand-off, abort semantics, and the unchanged
  non-interactive fallback. It replaces the phased-form requirements of
  the archived `init-form-ui` change, which were never synced into
  `openspec/specs/`.

### Modified Capabilities

None. `configure-and-credstore` describes the non-interactive text
prompts, which stay as they are. The wizard's behavior on a terminal is
specified in the new capability.

## Impact

- **Code:** `cmd/pmox/init_form.go`, `init_conn.go`,
  `init_discovery.go`, `init_ssh.go`, `init_persist.go`, `config.go`
  (`edit`), plus a new wizard model (proposed under
  `internal/tui/wizard` for the generic shell and `cmd/pmox` for the
  pmox-specific stages). `internal/tui` gains spinner/dialog helpers.
- **`internal/pvessh`:** `PromptAndPinHostKey` gets a non-interactive
  split (fetch fingerprint / append to known_hosts) so the app can ask
  in a dialog. The existing function stays for the linear path.
- **Dependencies:** `github.com/charmbracelet/bubbletea` and
  `github.com/charmbracelet/bubbles` become direct dependencies (both
  are already indirect through huh). Tests may add
  `github.com/charmbracelet/x/exp/teatest`.
- **Tests:** the stage-loop tests (`init_form_test.go`) move to
  model-level tests that drive the program with messages and stubbed
  async operations. Linear-path tests are untouched.
- **Docs:** README's `pmox init` walkthrough gets updated screenshots or
  wording.
