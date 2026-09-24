## ADDED Requirements

### Requirement: Phased form pages

`pmox init` SHALL, on a terminal, collect setup answers through a small
number of navigable form pages — Connection, Defaults, and Access — rather
than one prompt at a time. Within a page the user SHALL be able to move
between fields and change answers before the page is submitted.

#### Scenario: Connection page collects URL and token together

- **WHEN** `pmox init` starts on a terminal
- **THEN** a Connection page presents the API URL and the token source (generate or paste) with their fields, navigable before submitting

#### Scenario: Defaults page presents discovered options together

- **WHEN** the Connection page is submitted and discovery succeeds
- **THEN** a Defaults page presents node/template/storage/snippet-storage/bridge as selectable fields pre-filled from discovery

#### Scenario: Access page collects SSH key, user, and node SSH

- **WHEN** the Defaults page is submitted
- **THEN** an Access page collects the SSH key choice, default user, and node SSH credentials

### Requirement: In-page validation keeps the user on the page

`pmox init` SHALL validate the URL (reachability) and the credentials as
the Connection page submits, and on failure return the user to the page
with their prior answers pre-filled rather than aborting.

#### Scenario: Unreachable URL returns to the page

- **WHEN** the entered URL is not reachable
- **THEN** init reports the failure and returns to the Connection page with the entered values preserved

#### Scenario: Invalid credentials return to the page

- **WHEN** the token (or login) is rejected
- **THEN** init reports it and returns to the Connection page rather than exiting

### Requirement: Final review before writing

`pmox init` SHALL present a review screen listing every chosen value
(secrets masked) before persisting anything, and SHALL allow the user to
go back and change any answer. No configuration or secret is written until
the user confirms.

#### Scenario: Confirm writes the configuration

- **WHEN** the user confirms on the review screen
- **THEN** the config is written and the secret stored

#### Scenario: Back edits an answer before writing

- **WHEN** the user chooses to go back and change an answer
- **THEN** init re-collects that page and returns to review, having written nothing yet

#### Scenario: Editing the connection re-runs discovery

- **WHEN** the user goes back and changes the URL or token
- **THEN** init re-authenticates and re-discovers so the Defaults reflect the new connection

### Requirement: Non-interactive fallback is unchanged

`pmox init` SHALL, when input is non-interactive (`--no-input`, no TTY, or
`--output json`), not show forms and instead use the existing text-prompt
behavior or error with guidance, leaving scripted use unaffected.

#### Scenario: No TTY uses text prompts

- **WHEN** `pmox init` runs without a terminal
- **THEN** no form UI is shown and the prior non-interactive behavior applies
