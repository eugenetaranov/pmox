## Context

pmox's `create-template` needs to land a cloud-init user-data file in
a PVE storage pool's `snippets/` directory so the resulting template
boots with qemu-guest-agent pre-installed. Today it tries to use
`POST /nodes/{node}/storage/{storage}/upload` with `content=snippets`,
which PVE 8.x rejects server-side — the upload endpoint's content enum
is restricted to `iso, vztmpl, import` in stock PVE, confirmed against
a live cluster. The code path also mutates the cluster-wide storage
config to add `snippets` to the pool's content list before uploading,
which is a hard-to-reason-about side effect even when it works.

Every other tool in this space (bpg/proxmox Terraform, community
Ansible modules, Packer Proxmox plugin) writes snippets via SSH to
the pool's on-disk path. That's the transport pmox should adopt.

Proxmox permissions: a root-equivalent account on the PVE node is the
simplest thing that always works, because snippet directories often
need ownership/mode fixes after creation. The user explicitly asked
for password auth as the default (not everyone manages SSH keys on
their PVE host), with key-file as an opt-in for anyone who does.

## Goals / Non-Goals

**Goals:**
- Replace the HTTP upload path for snippets with SCP/SFTP over SSH.
- Prompt the user for SSH credentials during `pmox configure`, with
  password as the default auth method and key file as an option.
- Store SSH secrets in the OS keyring, same as the API token.
- Validate credentials at configure time, not at first-use time.
- Stop mutating PVE storage `content=` lists.
- Keep non-create-template commands working without SSH credentials —
  only create-template should require them.

**Non-Goals:**
- Supporting an `ssh-agent` auth source. (Trivial to add later;
  password+key covers the ask.)
- Supporting known_hosts TOFU with persistent pinning. v1 accepts a
  `--ssh-insecure` knob that skips host-key verification, defaulting
  to strict-with-prompt at configure time.
- Running arbitrary commands over the SSH session. Only file writes
  and `mkdir -p`, via the SFTP subsystem.
- Uploading ISO/vztmpl content via SCP. Those still work over the
  HTTP upload API and are unaffected.
- Retrofitting the non-create-template commands to need SSH.

## Decisions

### D1. SFTP, not scp/rsync shell-out

Use `github.com/pkg/sftp` on top of `golang.org/x/crypto/ssh`. Pure
Go, no shell dependency, runs identically on macOS and Linux, handles
mkdir/chmod/write atomically in one session. Alternative considered:
shell out to `scp` — rejected because it needs a password-prompting
tty for the password path, and because pmox's "single static binary"
promise means we can't rely on any CLI being installed on the user's
machine.

### D2. Where to write the file

PVE stores a storage pool's on-disk root in the `path` field returned
by `GET /storage/{storage}`. The snippets subdir is conventionally
`<path>/snippets/`. Code path:

```
GET /storage/local                  → path = "/var/lib/vz"
sftp: mkdir -p /var/lib/vz/snippets
sftp: write /var/lib/vz/snippets/pmox-bake-<hash>.yaml
```

`mkdir -p` via SFTP is `MkdirAll`, which the pkg/sftp client supports
natively. We do not chown/chmod; PVE reads the directory as root
anyway, and we land the file under the user that SSH'd in (typically
root), so it is readable by PVE's daemons.

Alternative considered: asking `GET /storage/{storage}/content` for
the snippets directory and inferring the path from a listed file.
Rejected — that endpoint returns `volid` strings, not filesystem
paths, and only lists files already present. Storage shape varies
(`dir`, `nfs`, `cifs`), and only `path` is returned uniformly.

### D3. Credential storage

Config YAML under `servers[<name>]` gains:
```yaml
ssh_user: root
ssh_auth: password   # or "key"
ssh_key: /Users/e/.ssh/pve_ed25519   # only when ssh_auth=key
```

Plus in the keyring, two new entries keyed on server name:
- `pmox.<server>.ssh_password` (password mode)
- `pmox.<server>.ssh_key_passphrase` (key mode, optional if the key
  is unencrypted)

The YAML file stays plaintext-safe — no secrets on disk.
`server.Resolved` gains a `SSHUser`, `SSHPassword`, `SSHKeyPath`,
`SSHKeyPassphrase` struct so callers get everything in one resolve.

Alternative considered: cramming the SSH credentials into a single
opaque blob in the keyring. Rejected because the resolve path already
fetches one secret per field, and matching that pattern keeps
credstore's API small.

### D4. Configure flow prompts

```
Proxmox node SSH username [root]: root
Authenticate with (p)assword or (k)ey file? [p]: p
Password: ••••••••
Verifying SSH connectivity to pve.example.com:22... ok
```

If `k`:
```
Path to SSH private key: /Users/e/.ssh/pve_ed25519
Key is passphrase-protected? [y/N]: n
Verifying SSH connectivity to pve.example.com:22... ok
```

Validation runs a real SSH handshake + `true` command (via SFTP stat
of `/`) and fails fast with a clear error on auth failure, host
unreachable, or host-key mismatch. The failure mode is a re-prompt,
same pattern as the existing API-token validation step.

### D5. Host key verification

v1 is strict-on-first-use via interactive prompt:

```
The authenticity of host 'pve.example.com (1.2.3.4)' can't be
established.
ED25519 key fingerprint is SHA256:abcd1234...
Are you sure you want to continue connecting (yes/no)? yes
```

The accepted fingerprint gets persisted to
`~/.config/pmox/known_hosts` (pmox-specific, not the user's
`~/.ssh/known_hosts` — we don't want to pollute user SSH state).
Subsequent connections verify against that file.

`--ssh-insecure` is a persistent root flag that skips host-key
verification. Rejected alternative: using the user's
`~/.ssh/known_hosts` directly. That's convenient but means a failed
PVE reinstall silently breaks pmox with a confusing error; the
separate file makes the scope explicit.

### D6. Resolved-path caching vs per-command resolve

`pmox create-template` is the only command that needs the storage
`path` field today. Rather than cache it in config, resolve it fresh
on every create-template run via one `GET /storage/{storage}` call.
That's one extra round trip per run — imperceptible next to the
multi-minute template build.

### D7. Package boundary: internal/pvessh

New package `internal/pvessh` exports:

```go
type Config struct {
    Host       string // "pve.example.com:22"
    User       string
    Password   string // exactly one of Password/KeyPath must be set
    KeyPath    string
    KeyPass    string // optional
    Insecure   bool
    KnownHosts string // path to pmox-managed known_hosts
}

func Dial(ctx context.Context, cfg Config) (*Client, error)

func (c *Client) UploadSnippet(ctx context.Context, storagePath, filename string, content []byte) error

func (c *Client) Ping(ctx context.Context) error

func (c *Client) Close() error
```

`Dial` does the SSH handshake + host-key check; `UploadSnippet`
reuses the open session for SFTP. `Ping` exists so configure can
validate without touching the filesystem.

### D8. Removal of UpdateStorageContent and UploadSnippet

Both methods on `pveclient.Client` go away along with their tests.
The cluster-wide content-list mutation was only ever a prerequisite
for the HTTP upload path; now that the upload path is gone, the
mutation is gone too. This is a clean removal, not a deprecation —
no released pmox version successfully shipped it.

### D9. Config migration for existing users

`pmox configure` is forward-compatible: re-running it on a configured
server walks the same prompts and overwrites fields that changed.
For users upgrading, `pmox create-template` detects missing SSH
fields on the resolved server and exits with:

```
create-template needs SSH access to the Proxmox node (for snippet
upload). Run 'pmox configure --server <name>' to add SSH credentials.
```

No automatic migration on first run — the user should consciously
choose between password and key.

## Risks / Trade-offs

- **Root password in keyring** → users who care can pick the key
  option; docs flag the tradeoff clearly.
- **Clusters with multiple nodes** → pmox targets one node (the API
  endpoint host) for SSH. Storage pools are cluster-wide, but snippet
  files written on one node typically replicate via shared storage
  (nfs/cifs/cephfs). For pure-local `dir` storage, the template
  build has to run on the same node the snippet lives on — but
  create-template already picks a single node, so this is inherent.
- **Host-key prompt on first configure** → interactive-only, which
  makes scripted/CI setup awkward. Mitigation: `--ssh-insecure`
  escape hatch for automation. Better: future change adds a
  `--ssh-fingerprint=SHA256:...` flag for explicit pinning.
- **`pkg/sftp` new dependency** → pure Go, well-maintained,
  permissively licensed (BSD-2). Small surface. Acceptable.
- **create-template now has two auth paths** (API token + SSH) →
  more to go wrong, more to validate. Mitigated by upfront
  validation in configure, so failures happen at setup time.

## Migration Plan

Not applicable — pre-v1, no released version has a working snippet
upload, so there's nothing to migrate *from*. Users upgrading will
see the "run pmox configure" message and update their server record
once.

## Open Questions

None blocking. Deferred to later changes:
- SSH agent support
- Non-interactive known-host pinning via CLI flag
- Key-file-only mode as a project-wide default
