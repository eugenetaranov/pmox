## Context

`pmox configure` (`cmd/pmox/configure.go` → `runInteractive`) prompts in a
fixed order: URL → overwrite check → token ID → token secret →
`validateCredentials` (first network contact) → discovery pickers.

Two rough edges:

1. `config.CanonicalizeURL` (`internal/config/config.go`) rejects any
   input whose scheme is not exactly `https`. A bare IP or hostname fails
   because `url.Parse("10.0.0.5")` yields an empty host (the value is
   parsed as a path).
2. The endpoint is not contacted until `validateCredentials`, which runs
   after both token prompts. Any failure returns an error and the command
   exits; the user cannot correct a mistyped address in place.

## Goals / Non-Goals

**Goals:**
- Accept the address a user naturally types (bare IP, hostname,
  `host:port`, IPv6, or a pasted web-UI URL) and normalize it.
- Detect an unreachable/incorrect endpoint at the URL step, before any
  credential prompt, and let the user re-enter it.
- Never send the token to a host not confirmed to be a PVE API.
- Remove prompts that have exactly one possible answer.

**Non-Goals:**
- Changing the stored canonical URL format or config.yaml shape.
- Auto-discovering the host (mDNS/scan) — the user still supplies an
  address.
- Probing multiple ports when none is given (we assume `8006`, the PVE
  default; an explicit port is always honored).
- Retry/re-ask behavior in the non-interactive path (it fails fast).

## Decisions

### 1. `CanonicalizeURL` becomes lenient
Rewrite to normalize rather than validate-and-reject:
- Trim input. If it contains no `://`, prepend `https://` before parsing
  (so `10.0.0.5`, `pve.lan`, `10.0.0.5:8007`, `[::1]:8006` all parse with
  a host).
- If the scheme is `http`, rewrite it to `https` and signal the caller to
  print a one-line note. Any scheme other than `http`/`https` is an error.
- Default the port to `8006` when absent; honor an explicit port.
- Lower-case the host; preserve IPv6 bracket form.
- Strip path/query/fragment; always emit `https://host:port/api2/json`.
- Return a small result (canonical string + `upgradedFromHTTP bool`) or
  keep the single-string signature and expose the http-upgrade note via a
  separate helper — implementation detail for the tasks phase, chosen to
  minimize churn at call sites (`configure`, `--remove`, tests).

### 2. Reachability probe runs at the URL step
Add an unauthenticated probe to `internal/pveclient` (e.g.
`Reachable(ctx, baseURL, insecure)`), a `GET /api2/json/version` with no
token and a short timeout (~5s, reuse `discoveryCtx`). Classify the
outcome into an enum the caller switches on:

| Class | Trigger | Configure action |
|-------|---------|------------------|
| `Reachable` | HTTP 200 or **401** (proves it's PVE) | proceed to token |
| `TLSUntrusted` | x509 / TLS verify error | existing insecure warning, then proceed |
| `Unreachable` | conn refused, timeout, no route, DNS failure | print specific error, **re-ask URL** |
| `NotPVE` | 2xx/4xx that isn't the PVE API shape (e.g. 404, HTML) | print error, re-ask URL |

A 401 is treated as success on purpose: it confirms a live PVE API
without any credentials. TLS handling mirrors the existing
`validateCredentials` fallback so the insecure decision stays a single,
logged choice; the probe records the resulting insecure flag and reuses
it for `validateCredentials` so we do not prompt/handshake twice.

### 3. URL re-ask loop
`runInteractive` calls a `promptReachableURL(ctx, p)` that loops:
prompt → canonicalize → probe. On `Unreachable`/`NotPVE` it prints the
classified message and loops. Blank input or Ctrl-C (EOF) returns a
clean abort (`ErrUserInput`) rather than looping forever. The old
fixed 3-attempt cap on pure syntax is replaced by this reachability
loop; genuinely unparseable input still prints the parse error and
re-prompts within the same loop.

### 4. Single-option auto-select
`pickNode`/`pickTemplate`/`pickStorage`/`pickBridge`: when the discovered
list has exactly one entry, skip the `tui.SelectOne` call, print e.g.
`Using node: pve (only option)`, and return it. Multiple options still
prompt. Honors `--no-input` (already returns the single/first value
without a TUI).

### 5. SSH key generate / select / browse

`promptSSHKey` currently suggests a default, shows a `huh` select of
`~/.ssh/*.pub`, and falls back to a path prompt. Lead instead with a
top-level choice:

- **Generate a new bootstrap key** — create an ed25519 keypair in pure Go
  (`crypto/ed25519` + `golang.org/x/crypto/ssh`: `MarshalPrivateKey` for
  the OpenSSH private blob, `MarshalAuthorizedKey` for the `.pub`). Write
  to a predictable, pmox-owned path — default `~/.ssh/pmox_ed25519`
  (`.pub` beside it), private `0600`, public `0644`, never overwriting an
  existing file without confirmation. Return the `.pub` path. This is the
  offered default when no key is configured yet.
- **Use an existing key** — the current `~/.ssh/*.pub` picker.
- **Browse…** — a `huh` file picker (or minimal directory-walk selector)
  rooted at `$HOME` for keys outside `~/.ssh`; selecting a private key
  offers its `.pub`, selecting a `.pub` uses it directly.

Only the **public** key path is stored in `ssh_pubkey`; pmox never needs
the private key (SSH auth uses the user's own agent/identity). A generated
private key is written for the user's convenience but is not tracked by
pmox beyond creation. `--no-input` keeps today's behavior (suggested
default / first key, no interactive branch, no generation).

## Risks / Trade-offs

- **Silent `http`→`https` upgrade** could mask a user genuinely pointing
  at a non-TLS proxy. Mitigation: print a one-line note when we upgrade;
  PVE's API is TLS-only on 8006 by design.
- **401-means-reachable** assumes the PVE API always requires auth on
  `/version`. That is true for PVE; a permissive reverse proxy returning
  200 is also accepted (still "reachable"). `NotPVE` catches non-API
  responses.
- **Extra round-trip**: the probe adds one unauthenticated request before
  the token prompt. It reuses the 5s discovery timeout and replaces the
  later blind failure, so net latency on the happy path is unchanged
  (the probe's TLS decision is reused by `validateCredentials`).
- **Loop with no cap** risks a stuck session on a wrong network;
  mitigated by the always-available blank/Ctrl-C abort.
