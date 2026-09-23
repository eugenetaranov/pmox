## ADDED Requirements

### Requirement: Token source choice in configure

Interactive `pmox configure` SHALL, after the API URL is confirmed and
before collecting a token, let the user choose between pasting an existing
API token and logging in to generate one. Non-interactive configure SHALL
keep the paste flow.

#### Scenario: User chooses to paste a token

- **WHEN** the user selects the paste option
- **THEN** configure prompts for a token ID and secret as before

#### Scenario: User chooses to generate a token

- **WHEN** the user selects the generate option
- **THEN** configure prompts to log in and creates the token via the API

### Requirement: Generate an API token via login

`pmox configure` SHALL, on the generate path, prompt for a `user@realm`,
a password, and a token name (defaulting to `pmox`), authenticate via the
PVE ticket endpoint, and create an API token with privilege separation
disabled (privsep=0) so it inherits the login user's privileges. The
returned token secret SHALL be stored via the secret store and the
returned full token id SHALL become the configured `token_id`.

#### Scenario: Successful generation completes configuration

- **WHEN** login succeeds and the token is created
- **THEN** configure stores the token secret, sets `token_id` to the returned full token id, and continues auto-discovery

#### Scenario: Token uses inherited privileges

- **WHEN** the token is created
- **THEN** it is created with privsep=0 (inherits the login user)

### Requirement: Password is never persisted

`pmox configure` MUST use the login password only to obtain the ticket and
MUST NOT write it to the config file, the secret store, logs, or debug
output. Only the generated token secret is persisted.

#### Scenario: Only the token secret is stored

- **WHEN** a token is generated
- **THEN** the secret store contains the token secret and the password appears in no persisted file or log

### Requirement: Token name collision is non-destructive

`pmox configure` SHALL, when the chosen token name already exists, report
the collision and re-prompt for a different name without deleting or
overwriting the existing token — because the token secret is shown only
once and cannot be re-fetched.

#### Scenario: Existing name re-prompts

- **WHEN** the chosen token name already exists on the server
- **THEN** configure reports it and asks for a different name, leaving the existing token untouched

### Requirement: pveclient ticket auth and token creation

`internal/pveclient` SHALL provide username/password ticket
authentication (`POST /access/ticket`) and API-token creation
(`POST /access/users/<userid>/token/<name>` with privsep=0), parsing the
returned full token id and secret, using the same TLS/insecure handling as
the API-token client.

#### Scenario: Login returns a ticket and CSRF token

- **WHEN** valid credentials are posted to the ticket endpoint
- **THEN** the ticket cookie and CSRF token are returned for the subsequent create call

#### Scenario: CreateToken returns the secret once

- **WHEN** a token is created with a valid ticket
- **THEN** the full token id and secret value from the response are returned to the caller
