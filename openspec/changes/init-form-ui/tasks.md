## 1. Orchestration + answer model

- [x] 1.1 Define typed answer structs (connection, defaults, access) and refactor `runInteractive` into phases: collectConnection → probe/auth → discover → collectDefaults → collectAccess → review → write
- [x] 1.2 Introduce form seams (`collectConnectionFn`/`collectDefaultsFn`/`collectAccessFn`/`reviewConfirmFn`) so orchestration is testable without a TTY
- [x] 1.3 Preserve the save path exactly (config write + credstore.Set) and the probe TLS-insecure reuse
- [x] 1.4 Tests: orchestration happy path, connection-failure loop, back-and-edit, re-discovery after connection edit (all via stubbed seams)

## 2. Connection page

- [x] 2.1 Build the Connection huh form: URL input, token-source select, conditional generate (user@realm/password/name) vs paste (id/secret) groups via WithHideFunc
- [x] 2.2 Canonicalize + probe on submit; reuse `validateCredentials`/`generateToken`; loop back with values pre-filled on failure
- [x] 2.3 Tests for the connection→auth wiring using the seam + httptest PVE stub

## 3. Defaults page

- [x] 3.1 Defaults phase (`collectDefaults`, behind a seam) discovers + selects node/template/storage/snippet-storage/bridge via the existing auto-selecting arrow-key pickers (node→template dependency handled by picking node first); reused rather than a single static form because their discovery + error-guidance sub-flows don't fit one huh form
- [x] 3.2 node→template dependency handled by resolving node before the dependent pickers
- [x] 3.3 Defaults auto-select covered by existing picker tests (`pickOneAuto`)

## 4. Access page + review

- [x] 4.1 Access phase (`collectAccess`, behind a seam) reuses `promptSSHKey` (generate/pick/browse), the default-user prompt, and `promptNodeSSH` (with its live handshake)
- [x] 4.2 Review screen: values listed (secrets masked) with Confirm / Edit-connection / Edit-defaults / Edit-access; only Confirm writes (via shared `persistServer`)
- [x] 4.3 Tests: confirm writes config+secret; back-to-defaults re-collects then confirm; cancel writes nothing (seam-driven)

## 5. Fallback, docs, validation

- [x] 5.1 Non-interactive/`--no-input`/`--output json` bypass forms and use the existing text prompts (or guided error)
- [x] 5.2 README + llms.txt: describe the form pages and review-before-write
- [x] 5.3 `openspec validate init-form-ui --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
