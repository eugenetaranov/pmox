## Why

`pmox create-template` cannot push its cloud-init bake snippet on most
PVE versions: the `POST /nodes/{node}/storage/{storage}/upload`
endpoint's `content` parameter has a hardcoded server-side enum of
`iso, vztmpl, import` and rejects `snippets` with a 400. The current
code path that enables `snippets` content on a dir-capable storage and
then uploads via the HTTP API is a dead end on any stock PVE 8.x. We
already confirmed this against a live cluster — the upload fails with
`value 'snippets' does not have a value in the enumeration 'iso,
vztmpl, import'`.

The fix is to stop using the HTTP upload endpoint for snippets and
instead SCP the file directly into the storage pool's on-disk
`snippets/` directory. This is how Terraform's bpg/proxmox provider,
Ansible's community.general.proxmox modules, and most hand-rolled
Packer templates already handle it, for exactly the same reason.

## What Changes

- Add a PVE-node SSH credential set to the configured-server record.
  Fields: `ssh_user` (default `root`), and one of `ssh_password` OR
  `ssh_key` (path to a private key). Secrets go in the keyring, not
  the YAML config file — same split as the API token.
- Prompt for the above during `pmox configure`: ask for username
  (default `root`), then ask whether to auth via password or key file,
  then capture that value. Password path is the default since the
  user specifically asked for it.
- Add `internal/pvessh` package wrapping `golang.org/x/crypto/ssh` and
  the `pkg/sftp` client, implementing `UploadSnippet(ctx, storage,
  filename, content)`. Resolves the storage pool's on-disk path via
  `GET /storage/{storage}` → `path` field, appends `/snippets/`,
  `mkdir -p` via sftp, then writes the file.
- **BREAKING** (internal, pre-v1): remove
  `pveclient.Client.UploadSnippet` and its multipart HTTP upload
  path. Callers switch to the new pvessh route.
- **BREAKING** (internal, pre-v1): remove
  `template.ensureSnippetsStorage` and its
  `UpdateStorageContent` call. No longer needed — SCP doesn't care
  what the PVE storage `content=` list allows, and mutating the
  cluster-wide storage config was always a side effect users didn't
  sign up for.
- `pmox create-template` now routes the bake snippet through
  `pvessh.UploadSnippet`. User-visible behavior: no more "enable
  snippets on local?" prompt; instead the SSH credentials gate gets
  validated up front, failing fast if neither password nor key works.
- `pmox configure` gains a validation step that opens an SSH session
  to the node and runs a harmless command (e.g. `true`) so users find
  out their password is wrong at configure time, not at create-template
  time.

## Capabilities

### New Capabilities
- `pve-node-ssh`: the SSH-to-PVE-node connection helper, credential
  storage, and `UploadSnippet` transport. Owns the `internal/pvessh`
  package and the new config fields under the server record.

### Modified Capabilities
- `configure-and-credstore`: `pmox configure` prompts for the new SSH
  username, auth mode (password/key), and value. Secrets land in the
  keyring alongside the existing API token. Adds a `ssh_user` field
  to the YAML server record.
- `pveclient-core`: removes `UploadSnippet` and
  `UpdateStorageContent`. No other endpoints change.

## Impact

- **Affected code**: `cmd/pmox/configure.go` (new prompts +
  validation), `internal/config/*` (new server fields),
  `internal/credstore/*` (new secret keys), `internal/template/*`
  (swap upload path), `internal/pveclient/storage.go` (delete
  `UploadSnippet` + `UpdateStorageContent`), new
  `internal/pvessh/` package, `cmd/pmox/create_template.go` (remove
  snippets-enable confirmation callback).
- **New dependencies**: `golang.org/x/crypto/ssh` (already in go.sum
  via launch SSH-wait) and `github.com/pkg/sftp`. Both pure-Go and
  permissively licensed.
- **Config migration**: existing configured servers lack the new SSH
  fields. On first `pmox create-template` after upgrade, detect the
  gap and prompt the user to run `pmox configure` to add them.
  Non-create-template commands (launch, list, info, start, stop,
  delete, clone) do NOT require SSH credentials.
- **Breaking?** Externally no — no released version shipped the
  upload-endpoint path working. Internally yes, two pveclient methods
  go away.
- **Security posture**: storing a root password in the OS keyring is
  meaningfully worse than a scoped API token, but (a) users asked for
  password support, (b) the keyring is the same store used for the
  API secret, and (c) the key-file path is available for users who
  care. We document the tradeoff in the configure command's help
  text and in README.
