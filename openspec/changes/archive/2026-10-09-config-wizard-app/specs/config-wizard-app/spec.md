## ADDED Requirements

### Requirement: Single persistent configuration app

On a terminal, `pmox init` and `pmox config edit` SHALL run their whole
interactive session as one terminal application. A header showing the
stages (Connection › Defaults › Access › Review) and the active stage's
subtitle SHALL stay fixed while the page below it is redrawn in place.
Moving between stages SHALL NOT print a new header or leave earlier
pages in the scrollback.

#### Scenario: Stage change redraws in place
- **WHEN** the user submits the Connection page and the wizard moves to Defaults
- **THEN** the header highlights Defaults and the page area shows the Defaults page
- **AND** no copy of the Connection page or header remains in the scrollback

#### Scenario: Config edit opens on Review in the same app
- **WHEN** `pmox config edit <context>` runs on a terminal and the stored credentials verify
- **THEN** the app opens with Review active and the current values listed

### Requirement: Asynchronous validation with inline feedback

The app SHALL run network and SSH operations (reachability probe,
login and token creation, credential check, certificate pin check,
discovery, SSH host-key fetch, SSH validation, and persisting) without
freezing input. It SHALL show a spinner with a label while each one
runs. A failure SHALL be shown on the page it belongs to, with every
answer on that page preserved.

#### Scenario: Unreachable URL stays on Connection
- **WHEN** the entered URL does not respond
- **THEN** the Connection page stays active with the URL and token answers still filled in
- **AND** the page shows the reachability error, including the underlying cause

#### Scenario: Rejected credentials stay on Connection
- **WHEN** the token is rejected or the login fails
- **THEN** the Connection page stays active with its answers preserved and the error shown

#### Scenario: Name collision on token creation
- **WHEN** the requested API token name already exists
- **THEN** the page shows that the name is taken and keeps the user on Connection to choose another

#### Scenario: Going back discards a stale result
- **WHEN** the user leaves a stage while one of its operations is still running
- **THEN** that operation's result SHALL NOT change the wizard's state when it arrives

### Requirement: Decisions as in-app dialogs

Every yes/no or trust decision during the interactive session SHALL be
shown as a dialog inside the app, never as a raw text prompt. Each
dialog SHALL keep its current safe default.

#### Scenario: Overwrite an existing server
- **WHEN** the entered URL is already configured and has not been confirmed this session
- **THEN** a dialog asks whether to overwrite it, defaulting to No
- **AND** declining exits with "aborted; no changes" before any token is created

#### Scenario: Changed TLS certificate
- **WHEN** the server presents a certificate whose fingerprint differs from the stored pin
- **THEN** a dialog shows both fingerprints and asks whether to trust and re-pin, defaulting to No
- **AND** declining aborts without sending any credential

#### Scenario: First-time SSH host key
- **WHEN** the node's SSH host key is not in pmox's known_hosts and `--ssh-insecure` is not set
- **THEN** a dialog shows the key type and SHA256 fingerprint and asks to trust it, defaulting to No
- **AND** accepting appends it to known_hosts before SSH validation runs

#### Scenario: Enable snippets on a storage
- **WHEN** no storage supports snippets but a directory-backed storage exists
- **THEN** a dialog offers to enable snippets on it, defaulting to Yes

#### Scenario: Regenerate drifted cloud-init
- **WHEN** saving finds an existing cloud-init file that authorizes a different SSH key
- **THEN** a dialog offers to regenerate it, defaulting to No

### Requirement: Defaults on one page

The Defaults stage SHALL present node, template, storage, snippet
storage and bridge on a single page, after loading the node's
templates, storages and bridges together. A field with exactly one
option SHALL be shown as a fixed value rather than a picker. Submitting
the page with a different node than the one loaded SHALL reload that
node's resources and show the page again instead of advancing. A field
whose list is empty or failed to load SHALL become a text input, with
the existing remediation guidance shown on the page.

#### Scenario: Single node is chosen silently
- **WHEN** the cluster has exactly one node
- **THEN** no node picker is shown and the page shows the node as a fixed value

#### Scenario: Changing the node reloads its resources
- **WHEN** the user picks a different node and submits the page
- **THEN** the templates, storages and bridges for the new node are loaded and offered, and the page stays on Defaults

#### Scenario: Missing permissions fall back to manual entry
- **WHEN** listing storage fails for the token
- **THEN** the storage field becomes a text input and the page shows the Datastore.Audit guidance

### Requirement: Access on one page

The Access stage SHALL hold the SSH public key choice (generate a
dedicated key, or pick an existing one, with a way back to that
choice), the default user, and the node SSH login (user, auth method,
and password or key path plus optional passphrase) on one page. Secrets
SHALL never be echoed. SSH validation SHALL run when the page is
submitted, and on failure the page SHALL stay active with the error
shown and answers preserved.

#### Scenario: SSH validation failure stays on Access
- **WHEN** the node SSH handshake fails with the entered password
- **THEN** the Access page stays active with the error shown and the non-secret answers preserved

### Requirement: Review, save and result summary

The Review stage SHALL list every chosen value with secrets masked and
offer Confirm, Edit connection, Edit defaults, Edit access, and Cancel.
Editing a stage after Review has been reached SHALL return to Review
when that stage is submitted. Nothing SHALL be written before Confirm.
On Confirm the app SHALL save with a spinner, then exit and leave a
result summary (configured server, config path, cloud-init outcome,
any warnings) in the terminal.

#### Scenario: Confirm saves and leaves a summary
- **WHEN** the user confirms on Review
- **THEN** the configuration and secrets are written
- **AND** after the app exits, the terminal shows the configured server, the config file path, and the cloud-init outcome

#### Scenario: Edit from Review returns to Review
- **WHEN** the user picks Edit defaults on Review and submits the Defaults page
- **THEN** the wizard returns to Review without passing through Access

#### Scenario: Cancel writes nothing
- **WHEN** the user picks Cancel on Review
- **THEN** the app exits with a cancellation error and nothing is written

### Requirement: Template build hand-off

The app SHALL, when the user chose to build a new template on the
Defaults page, save the configuration without a template, exit, and
then run the template build with its normal streaming output.

#### Scenario: Build runs after the app exits
- **WHEN** the user picked "Build a new Ubuntu template now" and confirmed on Review
- **THEN** the configuration is saved with no template, the app exits, and the template build starts

### Requirement: Back and abort keys

`Esc` on a page SHALL go to the previous stage (or to Review once
Review has been reached). `Esc` in a dialog SHALL choose the dialog's
safe default. `Ctrl-C` at any point SHALL cancel in-flight operations,
exit the app, and end the command with the interrupted exit code (130),
with nothing written unless saving had already completed.

#### Scenario: Ctrl-C during a probe
- **WHEN** the user presses Ctrl-C while the reachability probe is running
- **THEN** the probe is cancelled, the app exits, the command exits with code 130, and nothing is written

#### Scenario: Esc goes back
- **WHEN** the user presses Esc on the Access page before ever reaching Review
- **THEN** the Defaults page becomes active with its previous answers selected

### Requirement: Non-interactive path unchanged

`pmox init` SHALL NOT start the app when input is non-interactive
(`--no-input`, `PMOX_NO_INPUT`, no terminal, or `--output json`), and
SHALL produce the same prompts and output as before this change.

#### Scenario: Piped init keeps text prompts
- **WHEN** `pmox init` runs with stdin not a terminal
- **THEN** the linear text prompts are used and their output is identical to the previous release
