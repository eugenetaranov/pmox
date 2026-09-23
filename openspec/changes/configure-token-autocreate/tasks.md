## 1. pveclient: ticket auth + token creation

- [x] 1.1 Add `Login(ctx, baseURL, insecure, username, password) (Ticket, error)` → `POST /access/ticket`, parse `ticket` + `CSRFPreventionToken`
- [x] 1.2 Add `CreateToken(ctx, baseURL, insecure, ticket, userid, name)` → `POST /access/users/<userid>/token/<name>` (privsep=0) with cookie + CSRF header; parse `full-tokenid` + `value`
- [x] 1.3 Surface a distinct token-already-exists error; map other failures to the existing taxonomy; reuse the TLS/insecure config
- [x] 1.4 Tests: httptest PVE stub for ticket + create (success, bad credentials, name collision)

## 2. configure: token source choice + generate flow

- [x] 2.1 After URL confirm + overwrite check, add an interactive token-source choice (paste vs generate); non-interactive keeps paste
- [x] 2.2 Implement `generateToken`: prompt `user@realm` (validate `@`, suggest root@pam), password (PromptSecret, never stored), token name (always; default `pmox`)
- [x] 2.3 Call Login + CreateToken(privsep=0); set `token_id = full-tokenid`, secret = value; then continue the existing validate/store/config path
- [x] 2.4 On collision, re-prompt for a name; on auth failure, allow retry or fall back to paste
- [x] 2.5 Tests: generate path sets token_id/secret and stores only the secret; collision re-prompt; password never written (assert config + secret store contents)

## 3. Docs + validation

- [x] 3.1 README + llms.txt + docs/pve-setup.md: document the generate option, privsep=0/inherit, and that the password is never stored
- [x] 3.2 `openspec validate configure-token-autocreate --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
