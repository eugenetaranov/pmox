## Goals

1. `pmox configure` walks a homelab user through a one-time setup in
   under a minute, ending with a working, validated server entry on
   disk and a token secret in the system keychain.
2. The internal packages (`config`, `credstore`) expose exactly the
   surface every later slice needs to load credentials for a server URL,
   and nothing more.
3. The configure UX assumes the user has never seen the PVE API before:
   it lists available nodes, templates, storages, and bridges from the
   API itself rather than asking the user to memorize names.

## Non-goals

- Snippet-storage validation. Per
  [D-T2](../../decisions.md#d-t2-snippet-storage-validation-is-lazy),
  this happens lazily at `pmox launch --cloud-init` time, not here.
- A `--set-default` mechanism for picking a default server when
  multiple are configured. Per
  [D-T4](../../decisions.md#d-t4-server-resolution-logs-the-chosen-server-at--v),
  v1 ships only the 5-step resolution rule; a persisted default
  pointer can come later if needed.
- A full PVE API client. This slice ships exactly one endpoint
  (`GET /version`) — the rest comes in `pveclient-core`.
- Server-resolution logic itself. That's the next slice
  (`server-resolution`), which reads the config surface this slice
  produces.
- Editing existing servers in place. Re-running `configure` for the
  same URL prompts to overwrite; there is no `pmox configure --edit`.

## Decisions

### D1. URL canonicalization at configure time

The keychain account key is the API URL. Three different strings can
refer to the same server (`https://host:8006`,
`https://host:8006/api2/json`, `https://host:8006/api2/json/`), so
without canonicalization the same server collides as multiple keychain
entries.

**Decision**: at configure time, canonicalize to exactly
`https://<lowercase-host>:<port>/api2/json` — lowercase host, default
port `8006` if missing, drop trailing slash, force `/api2/json` path.
Reject anything else with a clear error:

- non-`https` scheme → `pmox requires https; got <scheme>://...`
- path other than `/api2/json` (or empty) → `pmox expects the API
  base URL; got path '<path>'. Use https://host:port/api2/json or
  https://host:port`

The canonicalized form is what goes in both the config-file map key
and the keychain account name. There is exactly one normalized form
per server, so duplicates can't happen.

### D2. `cmd/pmox/configure.go` as a sibling file

`configure` is large enough to deserve its own file: it has
interactive prompting, masked input, an HTTP call, four optional
auto-discovery calls, two flag-driven sub-modes (`--list`, `--remove`),
and the overwrite-prompt logic. Tack splits `vault.go` out of `main.go`
for similar reasons.

**Decision**: `cmd/pmox/configure.go` exists as a sibling to `main.go`.
`main.go` only adds `rootCmd.AddCommand(configureCmd)` in its `init()`
block; everything else lives in `configure.go`. This is the *first*
exception to the "everything in main.go" rule from slice 1's D1 — and
it's the only exception we expect until `launch.go` joins it later.

### D3. Config file is the source of truth for the server list

`go-keyring` can `Get`, `Set`, and `Delete` items by service+account,
but **cannot enumerate** items in a service on macOS Keychain. This
forces a constraint: pmox cannot ask the keychain "which URLs do you
know about?"

**Decision**: `~/.config/pmox/config.yaml` is the canonical list of
known servers. `pmox configure --list` reads this file, never the
keychain. `pmox configure --remove <url>` removes from both. The
`internal/credstore` package therefore exposes only `Get`, `Set`, and
`Remove` — no `List`.

**Edge cases this creates**:

| State                            | Behavior                                                                                       |
|----------------------------------|------------------------------------------------------------------------------------------------|
| config has URL, keychain doesn't | runtime error: `no token in keychain for <url>; re-run 'pmox configure' to set it`             |
| keychain has URL, config doesn't | invisible to pmox; orphan secret stays in user's keychain. Documented in the README.           |
| both have URL                    | normal                                                                                         |

### D4. TLS verification: try strict first, fall back to insecure with a loud warning

PVE installs in homelabs commonly use self-signed certs. The default
Go HTTP client refuses them, which would block most homelab users at
the very first command.

**Decision**: at configure time, attempt `GET /version` with full TLS
verification. On a TLS-specific error (cert verification failure,
unknown CA, hostname mismatch), automatically retry with
`InsecureSkipVerify: true`. If the retry succeeds:

1. Print a clearly-formatted warning to stderr:
   ```
   WARNING: TLS verification failed for <url>; falling back to insecure mode.
            Server certificate is not trusted, and pmox will not verify it
            on future requests. To re-enable verification later, edit
            ~/.config/pmox/config.yaml and set 'insecure: false' for this
            server.
   ```
2. Save the server with `insecure: true` in the config block.
3. Continue with the rest of configure normally.

If the insecure retry also fails (network error, 401, etc.), surface
the original (or the more-actionable of the two) error and abort.

**Why auto-fallback instead of opt-in `--insecure`**: pmox is a
homelab-first tool. Forcing every user to discover and remember a flag
on their first run is the kind of friction multipass-style tools
deliberately avoid. The warning is the contract: we don't silently
weaken security; we tell the user exactly what we did and how to undo it.

### D5. Token ID format validation at prompt time

PVE token IDs follow the format `user@realm!tokenname` (e.g.
`pmox@pve!homelab`). A typo here doesn't fail until the credential
validation hit, where the error is opaque (`401 Unauthorized`).

**Decision**: at prompt time, validate against the regex
`^[^@!]+@[^@!]+![^@!]+$`. On mismatch, re-prompt with:
`token ID must be in the form 'user@realm!tokenname' (got: '<input>')`.
Three failed attempts abort the configure flow with exit code
`ExitUserError`.

### D6. Auto-discovery via API after auth, with free-text fallback

After `/version` validates, configure makes up to four optional GET
calls to populate picker menus:

| Endpoint                                  | Populates       |
|-------------------------------------------|-----------------|
| `GET /nodes`                              | node menu       |
| `GET /nodes/{node}/qemu` (filter `template=1`) | template menu   |
| `GET /nodes/{node}/storage`               | storage menu    |
| `GET /nodes/{node}/network` (filter `type=bridge`) | bridge menu     |

Each call has a 5-second timeout and is independently optional. If
any one fails (permissions, API error, timeout), the corresponding
prompt falls back to a plain free-text input with no list. The
configure flow never aborts because of a discovery failure — only
because of `/version` auth failure.

The picker UX is:

```
Available nodes:
  1) pve1
  2) pve2
Default node [pve1]:
```

User can type a number, type a name verbatim, or hit enter for the
first entry. No fuzzy matching, no tab completion in v1.

The template picker also shows the VMID alongside the name:

```
Available templates:
  1) 9000  ubuntu-24.04-cloudinit
  2) 9001  debian-12-cloudinit
Default template [9000]:
```

Configure stores the VMID, not the name — VMIDs are stable across
renames.

### D7. Re-running configure with an existing URL prompts to overwrite

```
Server https://pve.home.lan:8006/api2/json is already configured.
Overwrite? [y/N]: _
```

Default is no. Anything other than `y`/`Y` aborts the flow with no
changes. **No `--force` flag** — configure is an interactive command
by definition; users who want non-interactive flow run
`pmox configure --remove <url>` first, then `configure` again.

### D8. Config file schema is flat per-server, no top-level defaults

```yaml
servers:
  "https://pve.home.lan:8006/api2/json":
    token_id: "pmox@pve!homelab"
    node: "pve1"
    template: "9000"
    storage: "local-lvm"
    bridge: "vmbr0"
    ssh_key: "~/.ssh/id_ed25519.pub"
    user: "ubuntu"
    insecure: false
```

URL as the YAML map key (quoted because it contains colons). Every
field except `token_id` is optional (can be empty string or omitted).
No top-level `defaults:` section — if cross-server defaults turn out
to be useful, they get added in their own slice.

**File permissions**: file mode `0600`, parent directory mode `0700`.
The file does not contain secrets, but it does contain server URLs
and token IDs, which leak some metadata.

**Path resolution**: `$XDG_CONFIG_HOME/pmox/config.yaml` if
`XDG_CONFIG_HOME` is set and non-empty, else `$HOME/.config/pmox/config.yaml`.
The `~` in `ssh_key` is expanded at read time, not stored expanded
(so the file remains portable across users).

### D9. SSH key default detection

Configure prompts: `Default SSH public key path [~/.ssh/id_ed25519.pub]:`
where the default is computed at prompt time:

1. If `~/.ssh/id_ed25519.pub` exists, suggest it.
2. Else if `~/.ssh/id_rsa.pub` exists, suggest it.
3. Else suggest no default (empty string in the brackets).

The user can type any path. Configure validates the file exists and
is readable; on failure, re-prompts with the error message. Three
failed attempts abort.

### D10. The `pveclient` package starts in this slice with one method

The full PVE client is its own slice (`pveclient-core`). But this
slice needs `GET /version` to validate credentials, plus the four
auto-discovery endpoints. Rather than inlining HTTP code in
`configure.go` and rewriting it later, ship a tiny `internal/pveclient`
now:

```
internal/pveclient/
  client.go    // HTTP client struct, ticket auth, request helper
  version.go   // GetVersion()
  nodes.go     // ListNodes(), ListTemplates(node), ListStorage(node),
               // ListBridges(node)
  errors.go    // typed errors used by exitcode mapping
```

Sized so the `pveclient-core` slice extends it with launch-time
methods (`NextID`, `Clone`, `Resize`, `SetConfig`, `Start`, `AgentNetwork`,
`Delete`) rather than rewriting it.

## Risks

- **`go-keyring` Linux quirks**: Linux Secret Service depends on a
  running `gnome-keyring-daemon` or `kwallet`. Headless Linux servers
  may have neither. Mitigation: catch the keychain error at `Set` time
  during configure and surface a clear message:
  `system keychain unavailable: <err>; pmox requires a running secret
  service (gnome-keyring or KWallet) on Linux.` Don't try to fall back
  to a file-based store — that's a footgun.
- **The picker menus are slow on cold caches**: four sequential GETs to
  the PVE API can take 1–2 seconds total. That's fine for a one-time
  command. Don't parallelize — sequential is easier to reason about.
- **Auto-fallback to insecure could mask a real MITM**: if an attacker
  intercepts the very first configure call and presents a fake cert,
  pmox accepts it and stores `insecure: true`. The warning print is
  the only signal. This is the explicit tradeoff in D4 — homelab UX
  beats hardening for an interactive setup command.
- **YAML map keys with colons require quoting**: most YAML libraries
  handle this, but a user who hand-edits the file might break it.
  Mitigation: the README explicitly warns against editing the file
  by hand and points at `pmox configure --remove` + re-`configure`
  as the supported flow.
