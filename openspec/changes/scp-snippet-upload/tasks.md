## 1. Dependencies and package scaffolding

- [x] 1.1 Add `github.com/pkg/sftp` to `go.mod` via `go get`; confirm `golang.org/x/crypto/ssh` is already present
- [x] 1.2 Run `go mod tidy` and commit go.mod/go.sum
- [x] 1.3 Create `internal/pvessh/` directory with `doc.go` and a package comment describing the snippet-upload-only scope

## 2. internal/pvessh — Config, Dial, Client

- [x] 2.1 Define `type Config struct { Host, User, Password, KeyPath, KeyPass string; Insecure bool; KnownHosts string }` in `internal/pvessh/pvessh.go`
- [x] 2.2 Define `type Client struct` wrapping `*ssh.Client` and `*sftp.Client`
- [x] 2.3 Implement `Dial(ctx context.Context, cfg Config) (*Client, error)` — validate exactly-one-of Password/KeyPath, build `ssh.ClientConfig` with the right `Auth` entries and `HostKeyCallback`, dial TCP honoring ctx deadline, handshake, open SFTP subsystem
- [x] 2.4 Implement password auth path using `ssh.Password(cfg.Password)`; ensure errors from `ssh.Dial` never include the password string
- [x] 2.5 Implement key auth path — read `KeyPath`, parse with `ssh.ParsePrivateKey` or `ssh.ParsePrivateKeyWithPassphrase` based on `KeyPass`, return typed error if key is encrypted but no passphrase supplied
- [x] 2.6 Implement host-key callback: when `Insecure` is true use `ssh.InsecureIgnoreHostKey()`, otherwise use `knownhosts.New(cfg.KnownHosts)` from `golang.org/x/crypto/ssh/knownhosts`
- [x] 2.7 Implement `(*Client).Close() error` closing SFTP then SSH
- [x] 2.8 Implement `(*Client).Ping(ctx) error` performing an SFTP `Stat("/")` and returning the error as-is

## 3. internal/pvessh — UploadSnippet

- [x] 3.1 Implement `(*Client).UploadSnippet(ctx context.Context, storagePath, filename string, content []byte) error`
- [x] 3.2 Compute `destDir = path.Join(storagePath, "snippets")` and call `sftp.MkdirAll(destDir)` (pkg/sftp provides this)
- [x] 3.3 Write to `destDir + "/." + filename + ".tmp"` first, then `Rename` to the final path for atomic replace
- [x] 3.4 Respect ctx cancellation: run the write in a goroutine and abort via closing the SFTP file if ctx fires; ensure the tmp file is removed on abort
- [x] 3.5 Return errors wrapped with the destination path for debuggability (without exposing credentials)

## 4. internal/pvessh — known_hosts management

- [x] 4.1 Add `PromptAndPinHostKey(ctx, host string, w io.Writer, r io.Reader, knownHostsPath string) error` helper — dials once with an accept-first HostKeyCallback, prints fingerprint, reads yes/no, appends to the known_hosts file on yes
- [x] 4.2 Add `knownHostsPath()` helper returning `$XDG_CONFIG_HOME/pmox/known_hosts` or `$HOME/.config/pmox/known_hosts`, creating parent dir with 0700 and file with 0600 on first write
- [x] 4.3 Ensure `PromptAndPinHostKey` NEVER touches `~/.ssh/known_hosts`

## 5. internal/pvessh — unit tests

- [x] 5.1 Add `internal/pvessh/pvessh_test.go` using an in-process SSH server (e.g. `golang.org/x/crypto/ssh` server side against a loopback listener with a test host key)
- [x] 5.2 Test `Dial` happy path with password auth
- [x] 5.3 Test `Dial` happy path with unencrypted ed25519 key
- [x] 5.4 Test `Dial` error on encrypted key without passphrase
- [x] 5.5 Test `Dial` error on wrong password — assert error does not contain the password
- [x] 5.6 Test `Dial` error on both Password and KeyPath set; and on neither set
- [x] 5.7 Test `Ping` succeeds, and fails when SFTP subsystem is not registered on the test server
- [x] 5.8 Test `UploadSnippet` creates `snippets/` dir if missing, writes the file, and the file contains exactly the supplied bytes — run against an SFTP server backed by `t.TempDir()`
- [x] 5.9 Test `UploadSnippet` overwrites an existing file atomically (simulate a pre-existing file, confirm it is replaced)
- [x] 5.10 Test `UploadSnippet` respects ctx cancellation — no partial file left at destination name
- [x] 5.11 Test host-key mismatch in strict mode returns an error identifying the mismatch
- [x] 5.12 Test `Insecure: true` skips host-key verification

## 6. Config and credstore changes

- [x] 6.1 Extend the YAML server record in `internal/config` with an optional nested `node_ssh` block containing `user`, `auth` (`"password"|"key"`), `key_path` (string, only for key mode)
- [x] 6.2 Update YAML marshal/unmarshal tests to cover round-tripping each SSH field combination
- [x] 6.3 Extend `server.Resolved` struct with `NodeSSHUser`, `NodeSSHAuth`, `NodeSSHPassword`, `NodeSSHKeyPath`, `NodeSSHKeyPassphrase` and a `HasNodeSSH()` helper
- [x] 6.4 Update server-resolution code to fetch the right secret from credstore based on `NodeSSHAuth`:
  - password mode → `credstore.GetNodeSSHPassword(url)`
  - key mode + encrypted → `credstore.GetNodeSSHKeyPassphrase(url)`
  - key mode + plain → no keyring call
- [x] 6.5 Add `credstore.SetNodeSSHPassword/GetNodeSSHPassword/RemoveNodeSSHPassword` and matching `*NodeSSHKeyPassphrase` variants
- [x] 6.6 Extend `configure --remove` path to also delete `node_ssh_password` and `node_ssh_key_passphrase` keyring entries, tolerating orphan entries
- [x] 6.7 Unit tests for the new credstore functions (happy path + missing key sentinel)
- [x] 6.8 Unit tests for `Resolve` covering password mode, key mode unencrypted, key mode encrypted, and legacy record with no SSH fields set

## 7. configure command — SSH prompts and validation

- [x] 7.1 In `cmd/pmox/configure.go`, after the existing API auto-discovery block, add prompts for `ssh_user` (default root), auth method (p/k, default p), and the corresponding secret
- [x] 7.2 Implement non-echoing password read using `golang.org/x/term` (already used elsewhere or bring it in)
- [x] 7.3 For key mode: prompt for key path, then `Key is passphrase-protected? [y/N]:`, then read passphrase if yes
- [x] 7.4 After collecting credentials, call `pvessh.PromptAndPinHostKey` if the host is not yet in pmox known_hosts (and `--ssh-insecure` is not set)
- [x] 7.5 After host-key pin, call `pvessh.Dial` + `(*Client).Ping` to validate; on failure print the error and re-prompt the auth method section
- [x] 7.6 On success, persist the YAML fields and the relevant keyring entries; print `Verifying SSH connectivity to <host>:22... ok`
- [x] 7.7 Update the existing `Successful first-time configure` unit test to cover the new prompts (stub `pvessh.Dial` via an interface seam if needed)
- [x] 7.8 Add an integration-style test covering the re-configure overwrite path including SSH secrets being rewritten
- [x] 7.9 Add a test for host-key first-seen prompt accept and reject

## 8. Root command — --ssh-insecure flag

- [x] 8.1 In `cmd/pmox/main.go`, add a persistent bool flag `--ssh-insecure` paired with env var `PMOX_SSH_INSECURE`
- [x] 8.2 Plumb the flag value into a package-level accessor (or the context) that `configure` and `create-template` read
- [x] 8.3 Emit a stderr warning on first use in the process when the flag is active
- [x] 8.4 Unit test: flag is parsed; env var equivalent is parsed; warning is printed exactly once

## 9. pveclient — remove dead methods

- [x] 9.1 Delete `UploadSnippet` from `internal/pveclient/storage.go` (or wherever it lives) and its tests
- [x] 9.2 Delete `UpdateStorageContent` from `internal/pveclient/storage.go` and its tests
- [x] 9.3 Remove any exported types/helpers only used by the deleted methods (multipart request helper if nothing else uses it)
- [x] 9.4 `go build ./...` to confirm no unreferenced compile errors; update call sites flagged

## 10. create-template — swap to pvessh

- [x] 10.1 In `internal/template/`, delete `ensureSnippetsStorage` and its tests; remove the `confirm func(string) bool` callback plumbed from `opts.ConfirmEnableSnippets`
- [x] 10.2 Add a new phase that calls `GET /storage/{storage}` and reads the `path` field; error cleanly if `path` is empty
- [x] 10.3 Replace the `pveclient.UploadSnippet` call with `pvessh.Dial` → `(*Client).UploadSnippet(ctx, storagePath, filename, content)` → `Close`
- [x] 10.4 At the top of `create-template`, short-circuit with a clear error if the resolved server lacks `SSHUser`/`SSHAuth` — point the user to `pmox configure`
- [x] 10.5 Remove `ConfirmEnableSnippets` from the `create-template` options struct and from `cmd/pmox/create_template.go` CLI wiring
- [x] 10.6 Update `internal/template/*_test.go`: drop the ensureSnippetsStorage tests, add a test that the new path resolver calls `GET /storage/{storage}` and threads the result into `pvessh.UploadSnippet` (use an interface seam to fake pvessh)
- [x] 10.7 Add a test that create-template fails fast with the expected message when SSH fields are missing on the resolved server
- [x] 10.8 Add a test that non-create-template commands (`launch`, `list`, `info`, `start`, `stop`, `clone`, `delete`) still succeed when SSH fields are absent — smoke test via the existing test harnesses

## 11. Docs

- [x] 11.1 Update `README.md` configure walkthrough to show the new SSH prompts and the host-key pinning step
- [x] 11.2 Document `--ssh-insecure` in README under the flags/env section
- [x] 11.3 Add a short "Security tradeoffs" paragraph explaining why SSH is needed for create-template and why password auth in the keyring is offered alongside key auth
- [ ] 11.4 Update `llms.txt` with the new `internal/pvessh` package entry and the removed `UploadSnippet`/`UpdateStorageContent` endpoints (skipped — no llms.txt in repo)
- [x] 11.5 Update `cmd/pmox/configure.go` `--help` long description to mention that SSH credentials are collected and validated

## 12. End-to-end verification

- [x] 12.1 `go vet ./...` clean
- [x] 12.2 `golangci-lint run --timeout=3m` clean
- [x] 12.3 `go test ./...` clean
- [ ] 12.4 Manual smoke test against a real PVE cluster: `pmox configure` → `pmox create-template` → confirm a snippet file appears in the storage pool's `snippets/` directory and the resulting template boots with qemu-guest-agent installed (requires live PVE cluster)
- [ ] 12.5 Manual smoke test: legacy server record (no SSH fields) — confirm `pmox launch` still works and `pmox create-template` fails with the expected re-configure message (requires live PVE cluster)
