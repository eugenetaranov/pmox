## 1. Dependencies

- [x] 1.1 `go get github.com/zalando/go-keyring`
- [x] 1.2 `go get golang.org/x/term`
- [x] 1.3 `go mod tidy`

## 2. internal/config

- [x] 2.1 Create `internal/config/config.go` with `type Config struct { Servers map[string]*Server }` and `type Server struct { TokenID, Node, Template, Storage, Bridge, SSHKey, User string; Insecure bool }`
- [x] 2.2 `func Path() (string, error)`: returns `$XDG_CONFIG_HOME/pmox/config.yaml` or `$HOME/.config/pmox/config.yaml`
- [x] 2.3 `func Load() (*Config, error)`: reads YAML; returns empty Config if file missing (not an error); errors on permissions or YAML parse failures
- [x] 2.4 `func (c *Config) Save() error`: creates parent dir with mode `0700` if missing, writes file with mode `0600`, atomic via temp-file-and-rename
- [x] 2.5 `func (c *Config) AddServer(url string, s *Server)`: adds or replaces; URL must already be canonicalized
- [x] 2.6 `func (c *Config) RemoveServer(url string) bool`: removes; returns true if a server was removed
- [x] 2.7 `func (c *Config) ServerURLs() []string`: returns sorted list of canonical URLs
- [x] 2.8 `func CanonicalizeURL(raw string) (string, error)`: enforces D1 — lowercase host, default port 8006, force `/api2/json` path, https only
- [x] 2.9 Tests: `config_test.go` with table-driven cases for `CanonicalizeURL` (valid, missing port, missing path, http://, junk path, mixed case host) and a YAML round-trip test
- [x] 2.10 Test: file permissions after `Save()` are `0600` and parent dir is `0700`

## 3. internal/credstore

- [x] 3.1 Create `internal/credstore/credstore.go` with `func Get(url string) (string, error)`, `func Set(url, secret string) error`, `func Remove(url string) error`. Service name constant: `const service = "pmox"`
- [x] 3.2 Wrap go-keyring errors with context — distinguish "not found" from "keychain unavailable"
- [x] 3.3 Define exported sentinel `var ErrNotFound = errors.New("token not found in keychain")` so callers can `errors.Is`
- [x] 3.4 On Linux, surface a friendly error when no Secret Service is available: `system keychain unavailable: %w; pmox requires gnome-keyring or KWallet on Linux`
- [x] 3.5 Tests: `credstore_test.go` using `keyring.MockInit()` from go-keyring, table-driven Get/Set/Remove cases
- [x] 3.6 Test: round-trip with a URL containing port and path
- [x] 3.7 Test: `Get` on missing URL returns `ErrNotFound`

## 4. internal/pveclient (minimal subset)

- [x] 4.1 Create `internal/pveclient/client.go`: `type Client struct { BaseURL string; TokenID string; Secret string; Insecure bool; HTTPClient *http.Client }`
- [x] 4.2 `func New(baseURL, tokenID, secret string, insecure bool) *Client`: builds HTTP client with `Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}` and a 10-second timeout
- [x] 4.3 `func (c *Client) request(ctx context.Context, method, path string, query url.Values) ([]byte, error)`: builds request, sets `Authorization: PVEAPIToken=<token_id>=<secret>` header, returns body or typed error
- [x] 4.4 Create `errors.go` with sentinels: `ErrUnauthorized`, `ErrNotFound`, `ErrAPIError`, `ErrTLSVerificationFailed`, `ErrNetwork`
- [x] 4.5 Map HTTP status codes to sentinels in `request()`: 401 → `ErrUnauthorized`, 404 → `ErrNotFound`, 5xx → `ErrAPIError`. Detect TLS errors by inspecting `*tls.CertificateVerificationError` and `x509.UnknownAuthorityError` from the underlying transport error
- [x] 4.6 Create `version.go`: `func (c *Client) GetVersion(ctx) (string, error)` — calls `GET /version`, returns `data.version` field or error
- [x] 4.7 Create `nodes.go`: `ListNodes(ctx) ([]Node, error)`, `ListTemplates(ctx, node) ([]Template, error)` (filter `template=1`), `ListStorage(ctx, node) ([]Storage, error)`, `ListBridges(ctx, node) ([]Bridge, error)` (filter `type=bridge`)
- [x] 4.8 Tests: `client_test.go` with a mock `httptest.Server` covering `request()` happy path, 401, 404, 500, and a TLS error injection
- [x] 4.9 Test: `GetVersion` parses a real PVE response payload (commit a fixture under `internal/pveclient/testdata/version.json`)
- [x] 4.10 NEVER log the token secret. Verify via test: `client_test.go` uses a custom transport that captures requests and asserts the secret is never written to a log buffer

## 5. cmd/pmox/configure.go

- [x] 5.1 Create `cmd/pmox/configure.go`. Declare `var configureCmd = &cobra.Command{...}`
- [x] 5.2 In `cmd/pmox/main.go`, add `rootCmd.AddCommand(configureCmd)` to the existing `init()` block
- [x] 5.3 Define flags: `--list` (bool), `--remove` (string)
- [x] 5.4 `runConfigure(cmd, args)` dispatches: if `--list`, run list mode; if `--remove`, run remove mode; else run interactive flow
- [x] 5.5 List mode: load config, print one URL per line, sorted; if empty print "no servers configured"
- [x] 5.6 Remove mode: canonicalize the supplied URL; load config; if not present print error and exit `ExitNotFound`; else delete from config, save, then `credstore.Remove(url)` (ignore `ErrNotFound`); print `removed <url>`
- [x] 5.7 Interactive mode step 1: prompt `Proxmox API URL: `, canonicalize, on error re-prompt up to 3 times then `ExitUserError`
- [x] 5.8 Step 2: load config, check if URL already exists, if so prompt `Server <url> is already configured. Overwrite? [y/N]: `, abort on no
- [x] 5.9 Step 3: prompt `API token ID: `, validate against regex per D5, re-prompt up to 3 times
- [x] 5.10 Step 4: prompt `API token secret: ` using `term.ReadPassword(int(syscall.Stdin))`, no echo. Reject empty input
- [x] 5.11 Step 5: build a `pveclient.Client` with `Insecure: false`, call `GetVersion(ctx)`. On TLS verification error (per D4), retry with `Insecure: true`. On success print the warning per D4 spec text
- [x] 5.12 Step 6: if any non-TLS error from `GetVersion`, abort with the appropriate exit code (`ExitUnauthorized` for 401, `ExitAPIError` for 5xx, `ExitNetworkError` for transport errors)
- [x] 5.13 Step 7 (auto-discover node): call `ListNodes(ctx)` with 5s timeout. On success show numbered picker. On failure fall back to plain free-text prompt. Default to first entry
- [x] 5.14 Step 8 (auto-discover template): call `ListTemplates(ctx, node)`. Picker shows `VMID  name`. Store the VMID
- [x] 5.15 Step 9 (auto-discover storage): call `ListStorage(ctx, node)`. Picker shows storage names
- [x] 5.16 Step 10 (auto-discover bridge): call `ListBridges(ctx, node)`. Picker shows bridge names
- [x] 5.17 Step 11: SSH key prompt per D9. Default detection logic: check `~/.ssh/id_ed25519.pub` then `~/.ssh/id_rsa.pub`. Validate file exists and is readable
- [x] 5.18 Step 12: prompt `Default user [ubuntu]: ` (free text, default `ubuntu`)
- [x] 5.19 Step 13: build `*Server` struct, call `cfg.AddServer(url, server)`, then `cfg.Save()`, then `credstore.Set(url, secret)`. If keychain set fails, attempt to revert config save (best-effort) and abort with `ExitGeneric`
- [x] 5.20 Step 14: print `configured server <url>` on success
- [x] 5.21 Tests: `configure_test.go` with the prompting layer behind an interface so a fake terminal driver can drive it. Cover URL canonicalization edge cases, token-ID validation re-prompt, overwrite-prompt accept/reject, TLS fallback warning text, list mode output, remove mode with present and missing URL

## 6. exit code wiring

- [x] 6.1 Extend `internal/exitcode/exitcode.go` `From(err)` to recognize: `pveclient.ErrUnauthorized` → `ExitUnauthorized`, `pveclient.ErrNotFound` → `ExitNotFound`, `pveclient.ErrAPIError` → `ExitAPIError`, `pveclient.ErrNetwork` → `ExitNetworkError`, `credstore.ErrNotFound` → `ExitNotFound`. Use `errors.Is` for each
- [x] 6.2 Test: `exitcode_test.go` table-driven over wrapped and unwrapped variants of each sentinel

## 7. Smoke tests against a real or mock PVE

- [x] 7.1 Run `pmox configure` interactively against a real PVE (or a `httptest.Server` mock) and confirm: file exists at correct path, file mode is `0600`, secret is in keychain, `pmox configure --list` prints the URL
- [x] 7.2 Re-run `pmox configure` with the same URL, confirm overwrite prompt appears, type `n`, confirm nothing changed
- [x] 7.3 Re-run with `y`, confirm fields update
- [x] 7.4 Run `pmox configure --remove <url>`, confirm both file entry and keychain entry are gone
- [x] 7.5 Configure a server with a self-signed cert, confirm the TLS-fallback warning prints and `insecure: true` ends up in the saved config

## 8. README placeholder update

- [x] 8.1 Append a one-line section to the placeholder README: "Currently the only working subcommand is `pmox configure`. See `openspec/changes/` for in-flight work." Real README still ships in `docs-and-llms-txt`
