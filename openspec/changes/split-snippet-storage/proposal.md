## Why

Slice 7 (`cloud-init-custom`) uses a single `server.Storage` field for
both the VM disk and the cloud-init snippet. In a real Proxmox cluster
that almost never works: VM disks live on LVM-thin / ZFS / Ceph
(`images,rootdir` only), while snippets live on a directory-backed
storage like `local`. Users who configure pmox against a sensible disk
storage (e.g. `vm-data`) hit this error on their first launch:

    storage "vm-data" does not have 'snippets' in its content types.

The error is accurate but the remediation is painful: either edit
`/etc/pve/storage.cfg` on the host (and it might be a backend that
can't host snippets at all), or pass `--storage local`, which also
redirects the VM disk onto `local`. Neither is what the user wants.

## What Changes

- `server` config SHALL gain a second storage field, `snippet_storage`,
  that names the storage where pmox uploads cloud-init snippets. It is
  independent from `storage` (VM disk). Existing configs without this
  field SHALL continue to parse; they fall back to `storage` for
  backwards compatibility and emit a one-shot migration hint.
- `pmox configure` SHALL pick the snippet storage automatically after
  picking the disk storage:
  - Enumerate `ListStorage` results whose `content` includes `snippets`.
  - Exactly one match → auto-select silently.
  - Multiple matches → TUI picker (default: first).
  - Zero matches → enter the "no snippet storage" flow below.
- **No-snippet-storage flow.** When no storage has `snippets` in its
  content types, `pmox configure` SHALL offer to enable `snippets` on
  an existing directory-backed storage:
  - List storages whose `type` is `dir` (plus `nfs`, `cifs`, `cephfs`
    — every backend PVE allows `snippets` on).
  - Prompt `no storage supports snippets. enable snippets on "<name>"? [Y/n]`,
    defaulting to `local` when present.
  - On confirmation, call a new `UpdateStorageContent` client method
    (PUT `/storage/{name}` with the existing `content=` list plus
    `snippets`) and save `<name>` as `snippet_storage`.
  - On decline, print the manual remediation (which file to edit, which
    line to change) and leave `snippet_storage` unset; configure still
    saves credentials so the user can re-run after fixing the host.
  - On zero directory-capable storages, print the same manual remediation
    without prompting.
- `pmox launch` and `pmox clone` SHALL gain a `--snippet-storage <name>`
  flag that overrides `server.SnippetStorage` for a single invocation.
  The flag is independent from `--storage`.
- The launch pipeline SHALL resolve snippet storage separately from
  disk storage. `ValidateStorage`, the snippet upload, and the
  `cicustom` value SHALL all use the resolved snippet storage.
  Disk-storage validation (`SupportsVMDisks`) is unchanged and
  continues to run against `opts.Storage`.
- **Launch SHALL upload the cloud-init snippet via SFTP, not via the
  PVE HTTP upload endpoint.** PVE's `POST /nodes/{node}/storage/{storage}/upload`
  endpoint hardcodes its `content` parameter to `iso, vztmpl, import`
  and rejects `snippets` with a 400 — the same dead end that
  `2026-04-12-scp-snippet-upload` already migrated `create-template`
  away from. The launch path SHALL mirror that fix:
  - `launch.Options` SHALL gain an `UploadSnippet func(ctx,
    storagePath, filename, content) error` callback.
  - `launch.Run` SHALL call `client.GetStoragePath(opts.SnippetStorage)`
    to resolve the on-disk path, then invoke
    `opts.UploadSnippet(ctx, storagePath, snippet.Filename(vmid),
    cloudInitBytes)`.
  - `cmd/pmox/launch.go` and `cmd/pmox/clone.go` SHALL lazily dial
    `pvessh` (mirroring `cmd/pmox/create_template.go`) and inject the
    upload closure. SSH credentials are required only at upload time;
    earlier failure modes still surface without an SSH handshake.
  - `pveclient.PostSnippet` SHALL be deleted. No call sites remain
    after this change.
  - Launch SHALL fail fast with a clear, actionable error when a
    server has no `node_ssh` configured (`run 'pmox configure' to add
    SSH credentials for snippet upload`).
- `cicustom` format stays `user=<snippet_storage>:snippets/<filename>`.
  `pmox delete`'s cleanup parser already extracts the storage from the
  `cicustom` value, so delete needs no functional change.
- If `snippet_storage` is empty at launch time (old config, missing
  field), pmox SHALL fall back to `opts.Storage` and emit a stderr
  warning: `warning: no snippet_storage configured; falling back to "%s".
  run 'pmox configure' to set it permanently`.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities

- `cloud-init-custom`: snippet storage is now a first-class config field
  separate from VM disk storage. Configure auto-detects or enables it.
  Launch/clone accept `--snippet-storage`. Validation and upload target
  the snippet storage, not the disk storage.

## Impact

- Affected code:
  - `internal/config/config.go` — new `SnippetStorage` field on `Server`.
  - `internal/pveclient/storage.go` — new `UpdateStorageContent` method
    (PUT `/storage/{name}`, form-encoded `content=`); `PostSnippet`
    deleted.
  - `cmd/pmox/configure.go` — new `pickSnippetStorage` helper and the
    "enable snippets on existing dir storage" prompt.
  - `cmd/pmox/launch.go`, `cmd/pmox/clone.go` — new `--snippet-storage`
    flag; lazy `pvessh` dial; `UploadSnippet` closure threaded through
    to `launch.Options`.
  - `internal/launch/launch.go`, `internal/launch/cloudinit.go` — use
    `opts.SnippetStorage` for validation and `cicustom`; `Run` resolves
    the on-disk storage path and uploads via the injected
    `UploadSnippet` callback.
  - Tests in each of the above.
- Affected specs: delta on `cloud-init-custom` covering the split, the
  configure-time enable flow, and the SFTP upload path.
- No migration of stored configs required; missing field falls back to
  `Storage` with a warning. Users re-running `pmox configure` pick up
  the new field cleanly. Servers without `node_ssh` configured will
  hit a clear "run 'pmox configure'" error on first launch — they are
  already in this state, this change just surfaces it earlier.
- Dependencies: none new. Reuses existing `ListStorage`, the TUI
  `SelectOne` / `Confirm` prompters, and the existing
  `internal/pvessh` package.
