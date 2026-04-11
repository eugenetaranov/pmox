## Why

Slice 5 ships built-in cloud-init only: a default user, the
configured SSH key, DHCP, `qemu-guest-agent`. That's enough for
"I want a disposable Ubuntu VM" but nothing else. Anyone who needs
specific packages, write_files, a custom runcmd, a non-default
network config, or an existing corporate base image — can't.

This slice adds `--cloud-init <file>` with **full-replace**
semantics (per D-T5): the user-supplied file becomes the VM's
entire user-data. Pmox does not merge, inject, or layer its
built-in defaults on top.

## What Changes

- Add `--cloud-init <path>` flag to `pmox launch` and `pmox clone`.
  When set, the launcher skips the built-in `ciuser`/`sshkeys`
  config keys entirely and uploads the provided file as a snippet
  to the configured storage, then sets `cicustom=user=<storage>:snippets/pmox-<vmid>-user-data.yaml`.
- Add a snippet-storage validator that runs **at launch time, not
  at configure time** (per D-T2). Before attempting the snippet
  upload, pmox checks that the resolved storage has `snippets` in
  its content types. On failure, it prints a specific actionable
  error: which storage, which content type is missing, the path
  `/etc/pve/storage.cfg`, both possible fixes (edit storage config
  or pass `--storage`), and a link to
  `https://pve.proxmox.com/wiki/Storage`.
- Add snippet upload to `internal/pveclient`: `PostSnippet(ctx, node, storage, filename, content)` hitting
  `POST /nodes/{node}/storage/{storage}/upload` with a multipart
  body. This is the only endpoint in pmox's client that uses
  multipart encoding rather than form-urlencoded.
- Add snippet cleanup to `pmox delete`: when a VM tagged `pmox` is
  destroyed, remove its snippet file if present. Best-effort; a
  failed cleanup logs a warning but doesn't fail the delete.
- Extend `pveclient` with `ListStorageContent(ctx, node, storage, contentFilter)` so pmox can enumerate snippet files owned by a VM (for cleanup) and validate the storage's content-type support before upload.
- Validate the uploaded file as plain UTF-8 text ≤ 64 KiB (PVE's
  snippet limit). Oversized or binary files fail before the
  upload attempt with a clear message.
- README gets a cloud-init section in slice 9 — not this slice.
  But this slice ships `examples/cloud-init.yaml` containing a
  minimal-but-correct snippet with an SSH key block, because
  D-T5's "operational consequence" is that users who forget the
  SSH key lock themselves out; the example is the mitigation.
- Validate the uploaded file **does** include an
  `ssh_authorized_keys:` entry when pmox detects no other auth
  path. Warning, not error — a user might deliberately want
  a password-only VM. Emit to stderr: `warning: --cloud-init file
  has no ssh_authorized_keys; you may not be able to SSH in`.
  Warning can be silenced with `--no-ssh-key-check`.

## Capabilities

### New Capabilities
- `cloud-init-custom`: the `--cloud-init` flag, the snippet upload
  flow, the storage-content validator, the snippet cleanup hook
  for delete, and the text/size/warning validators.

### Modified Capabilities
- `launch-default`: `launch.Run` gains a `CloudInitPath` field on
  `Options`. When set, the config phase routes through the
  snippet upload path instead of setting built-in cloud-init keys.
  The `sshkeys` and `ciuser` keys are NOT set.
- `list-info-lifecycle`: `pmox delete` gains a snippet-cleanup
  step after the destroy task completes.
- `pveclient-core`: new multipart-body transport for storage
  uploads, plus `PostSnippet`, `DeleteSnippet`, and
  `ListStorageContent` endpoints.

## Impact

- **New files**: `internal/snippet/snippet.go` (upload orchestration
  + validators), `internal/snippet/snippet_test.go`,
  `internal/pveclient/storage.go` (PostSnippet, DeleteSnippet,
  ListStorageContent), test siblings, `examples/cloud-init.yaml`.
- **Modified files**: `internal/pveclient/client.go` gains a
  `requestMultipart` sibling to `requestForm`.
  `internal/launch/launch.go` gains a branch in the config phase.
  `internal/launch/cloudinit.go` gains `BuildCustomKV` for the
  `cicustom` code path. `cmd/pmox/launch.go` and `cmd/pmox/clone.go`
  add the `--cloud-init` and `--no-ssh-key-check` flags.
  `cmd/pmox/delete.go` calls `snippet.Cleanup` after the destroy
  task.
- **New dependencies**: none. Go stdlib `mime/multipart` and
  `io.Pipe` for the multipart body builder.
- **Cross-slice contract**: the snippet filename convention
  `pmox-<vmid>-user-data.yaml` is frozen here — slice 6's delete
  hook relies on it, and any future slice that needs to list
  pmox-owned snippets will reuse it.
