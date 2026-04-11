## D1. Full replace, no merge — per D-T5

`--cloud-init <file>` is full-replace. The file contents become the
VM's user-data verbatim. Pmox does not inject `ciuser`, `sshkeys`,
or anything else on top.

```go
// internal/launch/launch.go — phase 5 branch
if opts.CloudInitPath != "" {
    content, _ := os.ReadFile(opts.CloudInitPath)
    snippet.Upload(ctx, opts.Client, opts.Node, opts.Storage, vmid, content)
    kv := cloudinit.BuildCustomKV(opts, vmid)  // sets cicustom, agent, memory, cores, name, ipconfig0
    opts.Client.SetConfig(ctx, node, vmid, kv)
} else {
    kv := cloudinit.BuildBuiltinKV(opts, vmid) // sets ciuser, sshkeys, agent, ...
    opts.Client.SetConfig(ctx, node, vmid, kv)
}
```

`BuildCustomKV` does **not** set `ciuser` or `sshkeys`. PVE's
`cicustom` takes precedence over those keys — mixing them is
confusing and PVE's behavior isn't documented clearly. Clean break:
if you pass `--cloud-init`, you own the user-data.

**Operational consequence (D-T5 explicit):** users who forget to
include `ssh_authorized_keys` in their file will boot an SSH-less
VM. Mitigation is D2 below (warning) + `examples/cloud-init.yaml`.

## D2. SSH-key warning — detect and warn, don't block

After reading the file, before uploading, run a shallow text check:

```go
if !bytes.Contains(content, []byte("ssh_authorized_keys:")) &&
   !opts.NoSSHKeyCheck {
    fmt.Fprintln(opts.Stderr, "warning: --cloud-init file has no ssh_authorized_keys; you may not be able to SSH in")
}
```

**Why warn, not error:** a password-only VM or a VM configured via
`write_files` with a key dropped elsewhere is legitimate. The user
may know what they're doing. We surface the risk and let them
decide.

**Why a string contains, not YAML parse:** YAML parsing pulls in
a dependency (`gopkg.in/yaml.v3` is already in-tree) but introduces
false negatives (the key may be under `users:` → `- name: foo` →
`ssh_authorized_keys:`, and a dumb text match catches that; a
structured parse that only checks `top.users[*].ssh_authorized_keys`
misses `#cloud-config` that uses `ssh_authorized_keys` at the top
level). The text check is coarse but catches the typical mistake.

**`--no-ssh-key-check`** silences it for the "I know what I'm doing"
case.

## D3. Snippet storage validation — lazy per D-T2

```go
// internal/snippet/snippet.go
func ValidateStorage(ctx, client, node, storage string) error {
    storages, err := client.ListStorage(ctx, node)  // already exists from slice 2
    // find the entry with .Storage == storage
    // check its Content field contains "snippets"
    // error if not
}
```

The error message, per D-T2:

```
storage "local" does not have 'snippets' in its content types.

  current content: iso,vztmpl,rootdir,images
  expected to include: snippets

fix options:
  1. edit /etc/pve/storage.cfg on the PVE host and add snippets
     to the content= line for this storage
  2. re-run with --storage <other-storage> pointing to a storage
     that supports snippets (see: pmox configure --list-storage)

see https://pve.proxmox.com/wiki/Storage for content-type details.
```

This runs once per launch, immediately before the upload. Not at
configure time — D-T2 is explicit.

`pmox configure` stays unchanged in this slice.

## D4. Multipart upload — `mime/multipart` + `io.Pipe`

PVE's `/nodes/{node}/storage/{storage}/upload` endpoint expects a
`multipart/form-data` request with fields:

- `content` — the content type (`snippets` for us)
- `filename` — the filename to save as
- `file` — the file payload (actual file body)

Standard library handles this:

```go
// internal/pveclient/storage.go
func (c *Client) PostSnippet(ctx context.Context, node, storage, filename string, content []byte) error {
    pr, pw := io.Pipe()
    mw := multipart.NewWriter(pw)
    go func() {
        defer pw.Close()
        defer mw.Close()
        mw.WriteField("content", "snippets")
        mw.WriteField("filename", filename)
        fw, _ := mw.CreateFormFile("file", filename)
        fw.Write(content)
    }()
    req, _ := http.NewRequestWithContext(ctx, "POST",
        c.BaseURL+fmt.Sprintf("/nodes/%s/storage/%s/upload", node, storage),
        pr)
    req.Header.Set("Authorization", c.authHeader())
    req.Header.Set("Content-Type", mw.FormDataContentType())
    req.Header.Set("Accept", "application/json")
    resp, err := c.http.Do(req)
    // ... same status-code switch as requestForm
}
```

**Why `io.Pipe` and a goroutine** — `multipart.Writer` can't produce
its boundary until it's been written to, and a preallocated
`bytes.Buffer` would hold the whole file in memory twice. Pipe-plus-
goroutine streams it. Our snippet files are ≤64 KiB so the buffer
approach would be fine, but pipe is idiomatic and avoids the double-
allocation pattern.

**Rejected:** adding a dedicated `requestMultipart` helper in
`client.go`. One caller, ~30 lines; inlining into `storage.go`
keeps `client.go` focused on the two common shapes (query + form).
If a second multipart caller shows up, factor then.

## D5. Snippet filename convention

```
pmox-<vmid>-user-data.yaml
```

Frozen. This is the filename used by:
- `snippet.Upload` when writing
- `snippet.Cleanup` when deleting (slice 6's delete flow)
- any future "list pmox-owned snippets" command

Storage path as seen by PVE: `<storage>:snippets/pmox-<vmid>-user-data.yaml`,
which expands to `/var/lib/vz/snippets/pmox-<vmid>-user-data.yaml`
on default directory storage.

**Vmid in the filename** rather than VM name because names can
collide and change; vmids can't.

**Rejected:** a hash of file contents. Harder to clean up
retroactively if something goes wrong, and the vmid is enough.

## D6. Size and text validation

```go
const maxSnippetBytes = 64 * 1024 // 64 KiB

func ValidateContent(content []byte) error {
    if len(content) == 0 {
        return errors.New("cloud-init file is empty")
    }
    if len(content) > maxSnippetBytes {
        return fmt.Errorf("cloud-init file is %d bytes; max 64 KiB", len(content))
    }
    if !utf8.Valid(content) {
        return errors.New("cloud-init file is not valid UTF-8")
    }
    return nil
}
```

**64 KiB** is PVE's snippet limit. Not documented anywhere we
trust; derived from the underlying storage-hook constraints. If
someone hits it, they can split the file into `write_files` +
a pmox-hosted http GET in `runcmd`, but that's out of scope for
v1.

**UTF-8 check** catches accidentally passing a binary file. The
`utf8.Valid` from stdlib is zero-cost on small inputs.

## D7. Delete hook — best effort

```go
// cmd/pmox/delete.go, after the destroy task wait succeeds
if err := snippet.Cleanup(ctx, client, node, storage, vmid); err != nil {
    fmt.Fprintf(os.Stderr, "warning: could not remove snippet for vm %d: %v\n", vmid, err)
}
```

`Cleanup` calls `DeleteSnippet(ctx, node, storage, "pmox-<vmid>-user-data.yaml")`.
If the file doesn't exist (e.g. the VM was launched without
`--cloud-init`), PVE returns 404 and `DeleteSnippet` returns
`ErrNotFound`, which `Cleanup` swallows as success.

**Which storage?** `pmox delete` doesn't know at delete time
whether the VM was launched with `--cloud-init` or which storage
it used. Two options:

- A) parse the VM's `cicustom` config value (`user=local:snippets/pmox-104-user-data.yaml`) to extract storage + filename
- B) try the configured default snippet storage, ignore 404

**Decision:** A. `GetConfig` already exists from slice 6. Parse
the `cicustom` field; if present, use its storage/filename
exactly (handles the case where the user launched with a non-
default `--storage` and we're now deleting via the default). If
absent, skip cleanup entirely.

Parser: `cicustom` is `user=<storage>:snippets/<filename>` (may
also include `meta=` and `network=` parts separated by `,`). We
only care about the `user=` part in this slice.

## D8. `DeleteSnippet` and `ListStorageContent`

Two more small client methods:

```go
func (c *Client) DeleteSnippet(ctx context.Context, node, storage, filename string) error
// DELETE /nodes/{node}/storage/{storage}/content/<storage>:snippets/<filename>

func (c *Client) ListStorageContent(ctx context.Context, node, storage, contentFilter string) ([]StorageContent, error)
// GET /nodes/{node}/storage/{storage}/content?content=<filter>
```

`ListStorageContent` returns the content list; only used in this
slice by `ValidateStorage`'s smoke test and by any manual debug.
Not strictly necessary — `ListStorage` from slice 2 already
exposes the content types — but useful for "what snippets do I
already have" queries later. Add it now because the file
(`storage.go`) has to exist anyway.

## D9. Testing — fake PVE multipart path

The existing `pvetest.fake` helper doesn't handle multipart
requests yet. Extend it with a `POST /nodes/{node}/storage/{storage}/upload`
handler that uses `r.ParseMultipartForm(1<<20)` and captures
the received fields.

Test cases in `internal/snippet/snippet_test.go`:
- `TestUpload_HappyPath`: snippet with SSH key, fake server
  captures the multipart body, assert filename and content match
- `TestValidate_EmptyFile`: empty bytes → error
- `TestValidate_Oversized`: 100 KiB of 'a' → error
- `TestValidate_BinaryFile`: invalid UTF-8 → error
- `TestValidateStorage_HappyPath`: storage with `snippets` in
  content → nil
- `TestValidateStorage_Missing`: storage without `snippets` → error
  message contains storage name and content list

Tests in `internal/launch/launch_test.go` gain:
- `TestRun_CustomCloudInit`: pass `CloudInitPath` set to a fake
  file, assert `PostSnippet` is called before `SetConfig`, and
  the `SetConfig` body contains `cicustom=user=<storage>:snippets/pmox-<vmid>-user-data.yaml`
- `TestRun_CustomCloudInit_NoSSHKeys`: assert the SetConfig body
  does NOT contain `sshkeys=` or `ciuser=`

## D10. Example file as a functional test gate

`examples/cloud-init.yaml` is shipped in this slice. A test in
`internal/snippet/snippet_test.go` reads the file and runs
`ValidateContent` + the SSH-key text check against it, asserting
no errors and no warning. This catches accidental regressions
where we break our own example.

```go
func TestExampleFileIsValid(t *testing.T) {
    content, err := os.ReadFile("../../examples/cloud-init.yaml")
    // ...
    if err := ValidateContent(content); err != nil { t.Fatal(err) }
    if !bytes.Contains(content, []byte("ssh_authorized_keys:")) {
        t.Fatal("example must include ssh_authorized_keys so the warning isn't in our own README")
    }
}
```
