## 1. Orchestration + answer model

- [ ] 1.1 Define typed answer structs (connection, defaults, access) and refactor `runInteractive` into phases: collectConnection → probe/auth → discover → collectDefaults → collectAccess → review → write
- [ ] 1.2 Introduce form seams (`collectConnectionFn`/`collectDefaultsFn`/`collectAccessFn`/`reviewConfirmFn`) so orchestration is testable without a TTY
- [ ] 1.3 Preserve the save path exactly (config write + credstore.Set) and the probe TLS-insecure reuse
- [ ] 1.4 Tests: orchestration happy path, connection-failure loop, back-and-edit, re-discovery after connection edit (all via stubbed seams)

## 2. Connection page

- [ ] 2.1 Build the Connection huh form: URL input, token-source select, conditional generate (user@realm/password/name) vs paste (id/secret) groups via WithHideFunc
- [ ] 2.2 Canonicalize + probe on submit; reuse `validateCredentials`/`generateToken`; loop back with values pre-filled on failure
- [ ] 2.3 Tests for the connection→auth wiring using the seam + httptest PVE stub

## 3. Defaults page

- [ ] 3.1 Run discovery once post-auth; build the Defaults huh form with node/template/storage/snippet-storage/bridge selects pre-filled; keep single-option auto-select
- [ ] 3.2 Handle node→template dependency (re-query on node change, or node select immediately before the page)
- [ ] 3.3 Tests for defaults resolution/auto-select

## 4. Access page + review

- [ ] 4.1 Build the Access huh form: SSH key (generate/pick/browse), default user, node SSH creds; validate node SSH via the existing handshake
- [ ] 4.2 Build the review screen (values listed, secrets masked) with Confirm / Back-to-<page>; only Confirm writes
- [ ] 4.3 Tests: review confirm writes; back re-collects and writes nothing until confirm

## 5. Fallback, docs, validation

- [ ] 5.1 Non-interactive/`--no-input`/`--output json` bypass forms and use the existing text prompts (or guided error)
- [ ] 5.2 README + llms.txt: describe the form pages and review-before-write
- [ ] 5.3 `openspec validate init-form-ui --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
