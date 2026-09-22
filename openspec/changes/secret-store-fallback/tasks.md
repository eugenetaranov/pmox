## 1. Backend abstraction in internal/credstore

- [x] 1.1 Define an unexported `backend` interface (`get(account) (string, error)`, `set(account, secret) error`, `remove(account) error`) and move the current go-keyring calls into a `keychainBackend` implementing it.
- [x] 1.2 Add keychain availability detection: a `keychainAvailable()` that does a sentinel set→get→delete on a reserved account, cached via `sync.Once`; treat any failure as unavailable.
- [x] 1.3 Add a `resolve()` that reads `PMOX_SECRET_STORE` (`auto`|`keychain`|`file`, default `auto`) and returns the active backend(s): keychain-first for `auto`, single backend for `keychain`/`file`; `keychain` mode returns a clear error when unavailable.

## 2. File backend

- [x] 2.1 Add `secretsPath()` → `<XDG_CONFIG_HOME or ~/.config>/pmox/secrets.yaml` (mirror `config.Path`/`CloudInitDir` XDG logic).
- [x] 2.2 Implement `fileBackend` over a `map[url]map[kind]value` YAML doc: parse the account string (URL + suffix) into (url, kind); atomic write (temp+rename), file `0600` inside dir `0700`; load-modify-save on set/remove.
- [x] 2.3 Ensure an empty/missing file reads as "not found" (return `ErrNotFound`) rather than erroring.

## 3. Dispatcher: package API

- [x] 3.1 Rewrite package-level `Get`/`Set`/`Remove` to dispatch through the resolver: `auto` Get tries keychain then file; `auto` Set writes keychain-if-available else file; single-backend modes use only their backend.
- [x] 3.2 Make `Remove` fan out to BOTH backends, swallowing not-found in each, so no stray secret remains regardless of where it was written.
- [x] 3.3 Keep the node-SSH helpers (`GetNodeSSHPassword`/`Set…`/`Remove…`, `…KeyPassphrase`) working unchanged on top of the new dispatch (they already delegate to `Get`/`Set`/`Remove`).
- [x] 3.4 Verify no secret value is ever written to stdout/stderr or wrapped into an error string in any backend.

## 4. Wire-through (no API changes expected)

- [x] 4.1 Confirm `cmd/pmox/configure.go` (save/remove/regen) works unchanged against the dispatcher; add nothing new unless a call path bypasses `credstore`.
- [x] 4.2 Confirm `internal/server/resolver.go` hydrate resolves secrets via the dispatcher on a host with no keychain (file fallback path).

## 5. doctor check

- [x] 5.1 Add a `secrets.backend` check to `cmd/pmox/doctor.go`: report keychain vs file; warn (plaintext on disk) when the file fallback is active; reuse the cached availability probe.

## 6. Tests

- [x] 6.1 Backend-selection tests for `auto`/`keychain`/`file` × keychain-available/unavailable (inject the availability probe + a fake keychain via `keyring.MockInit`).
- [x] 6.2 File-backend tests: round-trip by URL/kind, `0600`/`0700` perms, atomic overwrite, missing-file → not-found, no secrets in `config.yaml`.
- [x] 6.3 Tolerant-read test: secret in file, keychain "available" but empty → `auto` Get returns the file value.
- [x] 6.4 Removal test: secret in both backends → `Remove` clears both; missing entry tolerated.
- [x] 6.5 doctor test: file fallback → warn; keychain → pass.

## 7. Docs

- [x] 7.1 README: document `PMOX_SECRET_STORE`, the `secrets.yaml` fallback (0600, headless/CI), and the plaintext-on-disk tradeoff; add the env var to the environment table.
- [x] 7.2 llms.txt: add `PMOX_SECRET_STORE`, the fallback file, and the doctor `secrets.backend` check.
- [x] 7.3 ROADMAP: add a `secret-store-fallback` shipped entry.

## 8. Validate

- [x] 8.1 `openspec validate secret-store-fallback --strict` passes.
- [x] 8.2 `go test -race ./...`, `golangci-lint run ./...`, and the doc link checker pass.
