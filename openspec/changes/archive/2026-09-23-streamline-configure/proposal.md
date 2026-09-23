## Why

The first thing a new user does is run `pmox configure`, and the very
first prompt is the hardest: today `CanonicalizeURL` rejects everything
except a full `https://host:port` URL, so the natural input — a bare IP
(`10.0.0.5`) or hostname (`pve.lan`) — fails with "pmox requires https".
Worse, reachability is only tested *after* the user has typed a token ID
and secret, and any failure (host down, wrong port) aborts the whole
command instead of letting them fix the address in place. Setup should be:
type an address → Enter → paste a token → done.

## What Changes

- **Liberal URL input**: `CanonicalizeURL` accepts a bare IP, bare
  hostname, `host:port`, IPv6 (`[::1]:8006`), and full URLs. A missing
  scheme becomes `https`; a missing port becomes `8006`; an explicit port
  is honored. `http://` is upgraded to `https://` with a one-line note
  (PVE serves the API over TLS). Path/query/fragment are stripped as
  today (paste the web-UI URL and it still works).
- **Reachability probe before credentials**: after the URL is entered
  (and normalized), `configure` probes the endpoint with an
  unauthenticated request *before* asking for a token. It classifies the
  result:
  - network failure (connection refused / timeout / no route) → show a
    specific error and **re-ask the URL**;
  - TLS verification failure → the existing insecure-fallback warning,
    then continue;
  - a PVE API response (200/401) → reachable, proceed to the token;
  - reachable but not a PVE API (e.g. 404 / HTML) → error and re-ask.
- **Loop until reachable**: the URL step re-prompts on network failure
  until the host answers or the user aborts (blank line or Ctrl-C exits
  cleanly). The token is never sent to an unconfirmed host.
- **Silent single-option auto-select**: the node/template/storage/bridge
  discovery pickers pick the sole option automatically (printing what was
  chosen) instead of prompting when there is only one possible answer.
- **SSH key generate-or-select**: the SSH-pubkey step leads with a choice
  — *generate a new dedicated bootstrap keypair*, *pick an existing key*,
  or *browse the filesystem* for one. Generation writes an ed25519
  keypair (private `0600`, public `0644`) to a predictable path and uses
  its public half for cloud-init. The existing `~/.ssh/*.pub` picker
  remains as the "pick existing" branch, now with a file-browser escape
  for keys outside `~/.ssh`.
- **Flag / non-interactive parity**: the `--url` / non-interactive path
  uses the same normalization and the same classified probe error, but
  fails fast instead of entering the interactive re-ask loop.

No config.yaml shape change; the stored canonical URL format
(`https://host:port/api2/json`) is unchanged, so existing configs and
keychain entries keep working.

## Capabilities

### Modified Capabilities

- `configure-and-credstore` — URL normalization now accepts scheme-less
  and port-less input; a reachability probe runs before credential
  prompts with a re-ask loop; single-option discovery pickers
  auto-select.

## Impact

- `internal/config/config.go` — `CanonicalizeURL` rewrite (scheme/port
  inference, `http`→`https` upgrade, IPv6).
- `cmd/pmox/configure.go` — reorder `runInteractive` (probe after URL,
  before token); new probe + error-classification helper; URL re-ask
  loop; single-option auto-select in `pickNode`/`pickTemplate`/
  `pickStorage`/`pickBridge`; extend `promptSSHKey` with a
  generate/select/browse top-level choice.
- `internal/config` (or a small `internal/sshkey` helper) — generate an
  ed25519 OpenSSH keypair with correct file permissions.
- `internal/pveclient` — may add/expose an unauthenticated reachability
  check and error classification (network vs TLS vs non-PVE).
- Tests: `internal/config` (canonicalization table), `cmd/pmox`
  (probe classification, re-ask loop, auto-select).
- Docs: README + llms.txt configure section.
