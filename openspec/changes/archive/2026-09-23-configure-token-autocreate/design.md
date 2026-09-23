## Context

`pmox configure` currently prompts for a token ID and secret that the user
must have created in the PVE web UI (`configure.go` steps 3–4), then
validates them with `GetVersion`. `internal/pveclient` only does API-token
auth (`Authorization: PVEAPIToken=<id>=<secret>`); it has no
username/password path.

The PVE API supports:
- `POST /access/ticket` (form `username`, `password`) → `data.ticket`,
  `data.CSRFPreventionToken`.
- `POST /access/users/<userid>/token/<tokenid>` (auth via
  `Cookie: PVEAuthCookie=<ticket>` + `CSRFPreventionToken` header, form
  `privsep=0`) → `data.value` (the secret, shown once) and
  `data["full-tokenid"]` (e.g. `root@pam!pmox`).

## Goals / Non-Goals

**Goals:**
- Let the user generate the API token from `configure` via login.
- Never persist the password; store only the generated token secret.
- Keep the existing paste-a-token flow intact and default for
  non-interactive use.

**Non-Goals:**
- Role/ACL management — the token uses privsep=0 (inherits the user).
- Deleting or rotating existing tokens.
- A standalone `pmox token` command (this lives inside `configure`).

## Decisions

### 1. pveclient ticket auth + token create
Add to `internal/pveclient`:
- `Login(ctx, baseURL, insecure, username, password) (Ticket, error)`
  where `Ticket{Cookie, CSRF string}` — `POST /access/ticket`.
- `CreateToken(ctx, baseURL, insecure string, t Ticket, userid, name string) (fullTokenID, secret string, err error)`
  — `POST /access/users/<userid>/token/<name>` with `privsep=0`, sending
  the cookie + CSRF header; parses `full-tokenid` and `value`.
- These use their own request path (cookie/CSRF), separate from the
  API-token `request`. TLS/insecure handling mirrors `New`.
- A 5xx/4xx maps to the existing error taxonomy; a token-already-exists
  400 is surfaced distinctly so configure can re-prompt.

### 2. configure token-source choice
After `promptReachableURL` and the overwrite check, and before the token
prompts, in interactive mode present a choice:
- **Paste an existing token** → current `promptTokenID` + `promptSecret`.
- **Log in and generate** → `generateToken` (below).
Non-interactive configure (no TTY) keeps the paste path unchanged.

### 3. generateToken flow
1. Prompt `user@realm` (validate it contains `@`; suggest `root@pam`).
2. Prompt password via `PromptSecret` (never echoed, never stored).
3. Prompt token name (always; default `pmox`).
4. `Login` → `CreateToken(privsep=0)`.
5. On success: `tokenID = full-tokenid`, `secret = value`; proceed exactly
   as the paste path (validate with `GetVersion`, store secret, write
   config). The password variable is cleared/goes out of scope.
6. On name-collision error: report and loop back to step 3 (new name).
7. On auth failure: report and allow retry or fall back to paste.

### 4. Security invariants
- The password is only sent to `/access/ticket` over the same
  TLS/insecure decision the probe settled on; it is never written to
  config, keychain, secrets.yaml, logs, or debug output.
- Only the token secret is persisted, via the existing `credstore.Set`
  (keychain or file fallback) — no new storage path.

## Risks / Trade-offs

- **privsep=0 gives the token the user's full rights.** Documented; it is
  the pragmatic default (no single built-in role covers pmox's VM +
  datastore needs). A least-privilege role path is a future enhancement.
- **Ticket auth is a new code path** in pveclient. Mitigation: small, well
  -scoped functions with httptest coverage; reuse the TLS config builder.
- **Password handling.** Mitigation: keep it in a local variable passed
  only to `Login`; never place it in a struct that is persisted or logged;
  test that configure never writes it.
- **Realm variety** (pam/pve/ldap). The flow just forwards `user@realm`
  and password to `/access/ticket`, which handles the realm — no special
  casing needed.
