## 1. Lenient URL normalization

- [x] 1.1 Rewrite `config.CanonicalizeURL` to prepend `https://` when the input has no scheme, so bare IP / hostname / `host:port` / IPv6 parse with a host
- [x] 1.2 Default the port to `8006` when absent; honor an explicit port; lower-case host; preserve IPv6 bracket form; strip path/query/fragment
- [x] 1.3 Upgrade `http` scheme to `https` and surface a one-line note to the caller (return value or companion helper); reject schemes other than http/https
- [x] 1.4 Update/extend `internal/config` tests with a normalization table (bare IP, hostname, host:port, IPv6, http upgrade, web-UI paste, unparseable)

## 2. Reachability probe + error classification

- [x] 2.1 Add an unauthenticated reachability check to `internal/pveclient` (e.g. `Reachable(ctx, baseURL, insecure)`) hitting `GET /api2/json/version` with a short timeout
- [x] 2.2 Classify outcomes into Reachable (200/401) / TLSUntrusted / Unreachable (conn refused, timeout, no route, DNS) / NotPVE
- [x] 2.3 Reuse the TLS-insecure decision from the probe for the later `validateCredentials` so the user is not warned/handshaked twice
- [x] 2.4 Unit-test the classifier against representative errors/responses (httptest server + injected dial errors)

## 3. Configure flow reorder + re-ask loop

- [x] 3.1 Add `promptReachableURL(ctx, p)` that loops prompt → canonicalize → probe, printing the classified message and re-asking on Unreachable/NotPVE
- [x] 3.2 Blank line or EOF/Ctrl-C at the URL prompt aborts cleanly with `ErrUserInput`
- [x] 3.3 Reorder `runInteractive`: URL+probe (before overwrite check and token prompts); pass the probe's insecure flag forward
- [x] 3.4 Non-interactive/flag path uses the same normalization + probe but fails fast (no loop)
- [x] 3.5 Tests: re-ask-then-succeed, abort-on-blank, non-interactive fail-fast

## 4. Single-option auto-select

- [x] 4.1 In `pickNode`/`pickTemplate`/`pickStorage`/`pickBridge`, auto-select and print when exactly one option exists; prompt otherwise
- [x] 4.2 Tests for single-option auto-select and multi-option still-prompts

## 5. SSH bootstrap key generate / select / browse

- [x] 5.1 Add an ed25519 OpenSSH keypair generator (pure Go: `crypto/ed25519` + `ssh.MarshalPrivateKey`/`ssh.MarshalAuthorizedKey`); write private `0600`, public `0644`, no clobber without confirm
- [x] 5.2 Rework `promptSSHKey` to lead with a top-level choice: Generate new / Use existing / Browse
- [x] 5.3 Wire "Generate" to the generator (default path `~/.ssh/pmox_ed25519.pub`) and return the public key path
- [x] 5.4 Add a "Browse…" filesystem picker (huh file picker or directory-walk) rooted at `$HOME`; resolve a selected private key to its `.pub`
- [x] 5.5 Preserve `--no-input` behavior (suggested/first key, no generation, no picker)
- [x] 5.6 Tests: generation writes correct perms + no-clobber; select existing; browse resolves `.pub`; no-input path

## 6. Docs + validation

- [x] 6.1 Update README + llms.txt configure section (accepted URL forms, probe-before-token, auto-select, SSH key generate/select/browse)
- [x] 6.2 `openspec validate streamline-configure --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
