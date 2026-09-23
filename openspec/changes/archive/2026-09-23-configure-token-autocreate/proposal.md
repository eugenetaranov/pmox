## Why

Creating a PVE API token by hand is the fiddliest part of first-time
setup: the user has to open the web UI, navigate to Datacenter →
Permissions → API Tokens, create one, remember to uncheck Privilege
Separation, and copy a secret shown only once. pmox can do this for them:
if the user prefers, `pmox configure` can log in with a username and
password, create the token over the API, store the secret, and finish —
no web UI trip.

## What Changes

- **`configure` offers a token source choice** (interactive): after the
  URL is confirmed, the user chooses to *paste an existing token* (today's
  flow) or *log in and generate one*.
- **Generate path**: prompt for `user@realm` and password, plus a token
  name (always prompted; default suggestion `pmox`). pmox authenticates
  via `POST /access/ticket`, then creates the token via
  `POST /access/users/<user>/token/<name>` with **privilege separation
  off** (privsep=0) so it inherits the login user's privileges. The
  returned secret is stored in the secret store; the resulting
  `full-tokenid` becomes the configured `token_id`.
- **Password is never persisted**: it is used once to obtain the login
  ticket and then discarded. Only the generated token secret is written
  (to keychain or the file fallback).
- **Name collision handling**: the token secret is shown only once and
  cannot be re-fetched, so if the chosen name already exists pmox reports
  it and re-prompts for a different name (it never deletes or overwrites
  an existing token).
- **New pveclient capability**: ticket (username/password) authentication
  and the token-create endpoint, kept separate from the existing
  API-token client.

## Capabilities

### New Capabilities

- `token-autocreate` — ticket-based login and API-token creation in
  pveclient, and the `configure` token-source choice + generate flow.

## Impact

- `internal/pveclient` — new ticket auth (`POST /access/ticket`) and
  `CreateToken` (`POST /access/users/<user>/token/<name>`, privsep=0),
  parsing `full-tokenid` + `value`.
- `cmd/pmox/configure.go` — token-source choice; generate flow (prompt
  user@realm + password + name, call CreateToken, store secret, set
  token_id); collision re-prompt. Non-interactive configure keeps the
  paste path.
- Docs: README + llms.txt + docs/pve-setup.md (mention the generate
  option; the password is never stored).
- Tests: ticket + CreateToken against an httptest PVE stub; collision
  re-prompt; password-not-stored invariant.
