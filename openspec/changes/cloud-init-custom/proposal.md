## Why

Slice 5 shipped built-in cloud-init only: a default user, the
configured SSH key, DHCP, `qemu-guest-agent`. That's enough for
"I want a disposable Ubuntu VM" but nothing else. Anyone who needs
specific packages, write_files, a custom runcmd, a non-default
network config, or an existing corporate base image — can't.

The earlier iteration of this slice added `--cloud-init <path>`
as an opt-in flag with full-replace semantics, so two code paths
coexisted: built-in (set `ciuser`/`sshkeys`) and custom (upload
a snippet and set `cicustom`). That split was ergonomic enough
for the flag case but meant that day-one users had no way to
discover how to customize without reading docs, and pmox
maintained two diverging cloud-init code paths.

This slice collapses the two paths. `pmox configure` always
generates a starter cloud-init file per-server the first time a
server is configured. `pmox launch` and `pmox clone` always
upload that file as a snippet and set `cicustom` — the built-in
path is removed entirely. When a user needs to customize
packages, users, runcmd, or network config, they edit the file
on disk and relaunch. When they need different config on the same
server, they terminate the VM, edit the file, spin up a new one.

## What Changes

- `pmox configure` gains a cloud-init generation step. After
  collecting the SSH key and default user, it writes a starter
  cloud-init file to `~/.config/pmox/cloud-init/<slug>.yaml`
  (where `<slug>` is derived from the canonical server URL).
  The starter is seeded from a frozen in-binary template with
  the configured SSH pubkey and default user substituted. If a
  file already exists at that path, configure leaves it alone
  and prints `cloud-init template already exists at <path> — not
  overwriting`.
- `pmox configure --regen-cloud-init` — new subflag that skips
  the full reconfigure flow, loads the existing server config,
  and rewrites the cloud-init file from the template (prompts
  once for overwrite confirmation if the file exists). Used for
  "my file is missing" and "reset my customizations" cases.
- `pmox launch` and `pmox clone` always read the per-server
  cloud-init file, validate it, upload it via `PostSnippet`, and
  set `cicustom`. The built-in `ciuser`/`sshkeys` code path is
  removed: `BuildBuiltinKV` is deleted and `BuildCustomKV`
  becomes the only config-builder.
- If the cloud-init file is missing at launch time, the launcher
  returns an error naming the path and suggesting `pmox configure
  --regen-cloud-init` to regenerate a sane default. The user can
  also create the file manually.
- Add a snippet-storage validator that runs at launch time, not
  at configure time (per D-T2). Before attempting the snippet
  upload, pmox checks that the resolved storage has `snippets`
  in its content types. On failure, it prints a specific
  actionable error: which storage, which content type is
  missing, the path `/etc/pve/storage.cfg`, both possible fixes
  (edit storage config or pass `--storage`), and a link to
  `https://pve.proxmox.com/wiki/Storage`.
- Add snippet upload to `internal/pveclient`: `PostSnippet(ctx,
  node, storage, filename, content)` hitting `POST
  /nodes/{node}/storage/{storage}/upload` with a multipart body.
  This is the only endpoint in pmox's client that uses multipart
  encoding rather than form-urlencoded.
- Add snippet cleanup to `pmox delete`: when a VM tagged `pmox`
  is destroyed, parse its `cicustom` value and remove the
  referenced snippet. Best-effort; a failed cleanup logs a
  warning but doesn't fail the delete.
- Extend `pveclient` with `ListStorageContent(ctx, node, storage,
  contentFilter)` so pmox can enumerate snippet files owned by a
  VM (for cleanup) and validate the storage's content-type
  support before upload.
- Validate the uploaded file as plain UTF-8 text ≤ 64 KiB (PVE's
  snippet limit). Oversized or binary files fail before the
  upload attempt with a clear message.
- Validate the uploaded file includes an `ssh_authorized_keys:`
  entry. Warning, not error — a user might deliberately want a
  password-only VM. Emit to stderr: `warning: cloud-init file
  <path> has no ssh_authorized_keys; you may not be able to SSH
  in`. Unconditional; there is no silencer flag.
- Ship `examples/cloud-init.yaml` as the frozen template source
  used by `pmox configure` to seed new files. It contains a
  minimal-but-correct snippet with an SSH key placeholder,
  default user, `qemu-guest-agent`, and a `runcmd` stub.
- README gets a cloud-init section in slice 9 — not this slice.

## Capabilities

### New Capabilities
- `cloud-init-custom`: configure-time file generation (initial
  write + `--regen-cloud-init`), the snippet upload flow, the
  storage-content validator, the snippet cleanup hook for delete,
  the text/size/warning validators, and the missing-file error
  path.

### Modified Capabilities
- `launch-default`: `launch.Run` always routes through the
  snippet upload path. `BuildBuiltinKV` is removed. The `sshkeys`
  and `ciuser` config keys are no longer set by pmox. The
  resolved cloud-init path comes from the server config, derived
  from the canonical URL.
- `list-info-lifecycle`: `pmox delete` gains a snippet-cleanup
  step after the destroy task completes. Every pmox-launched VM
  has a `cicustom` value, so cleanup runs for every delete (not
  conditional on opt-in).
- `pveclient-core`: new multipart-body transport for storage
  uploads, plus `PostSnippet`, `DeleteSnippet`, and
  `ListStorageContent` endpoints.

## Impact

- **New files**: `internal/snippet/snippet.go` (upload
  orchestration + validators), `internal/snippet/snippet_test.go`,
  `internal/pveclient/storage.go` (PostSnippet, DeleteSnippet,
  ListStorageContent), test siblings, `examples/cloud-init.yaml`,
  `internal/config/cloudinit.go` (resolver + generator seeded
  from the example).
- **Modified files**: `internal/pveclient/client.go` gains a
  `requestMultipart` sibling to `requestForm`.
  `internal/launch/launch.go` loses the built-in branch — the
  config phase always uploads a snippet and calls
  `BuildCustomKV`. `internal/launch/cloudinit.go` loses
  `BuildBuiltinKV`. `cmd/pmox/configure.go` gains a generation
  step at the end of `runInteractive` and a new `--regen-cloud-init`
  handler. `cmd/pmox/delete.go` calls `snippet.Cleanup` after
  the destroy task.
- **Removed surface**: `--cloud-init <path>` flag, `--no-ssh-key-check`
  flag, and `launch.Options.NoSSHKeyCheck` field are not part of
  this slice's final shape. If any were introduced during the
  earlier iteration they are reverted here.
- **New dependencies**: none. Go stdlib `mime/multipart` and
  `io.Pipe` for the multipart body builder.
- **Cross-slice contract**: the snippet filename convention
  `pmox-<vmid>-user-data.yaml` is frozen here — slice 6's delete
  hook relies on it, and any future slice that needs to list
  pmox-owned snippets will reuse it. The per-server slug scheme
  (`<host>-<port>`) is also frozen — credstore and any future
  per-server on-disk artifacts will reuse it.
