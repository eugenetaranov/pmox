## D1. Single code path — no built-in branch

Every pmox-launched VM gets its user-data from a snippet file on
disk. There is no built-in `ciuser`/`sshkeys` path anymore. The
launcher's config phase is straight-line:

```go
// internal/launch/launch.go — config phase
path := opts.CloudInitPath // resolved from server config before Run()
content, err := os.ReadFile(path)
if err != nil {
    return fmt.Errorf("read cloud-init file %s: %w\n  hint: run 'pmox configure --regen-cloud-init' to write a fresh default", path, err)
}
if err := snippet.ValidateContent(content); err != nil { return err }
if err := snippet.ValidateStorage(ctx, client, node, opts.Storage); err != nil { return err }
warnIfNoSSHKey(opts.Stderr, path, content)
if err := snippet.Upload(ctx, client, node, opts.Storage, vmid, content); err != nil { return err }
kv := cloudinit.BuildCustomKV(opts, vmid) // sets cicustom, agent, memory, cores, name, ipconfig0
return client.SetConfig(ctx, node, vmid, kv)
```

`BuildBuiltinKV` is deleted. `BuildCustomKV` becomes the only
config-builder. The launcher never sets `ciuser`, `cipassword`,
or `sshkeys` on a pmox-managed VM. Those concerns live in the
user's cloud-init file.

**Why collapse the branch:** two paths that diverge subtly is
worse than one path that's always explicit. The generated starter
file already contains the SSH key and default user — for the
"disposable Ubuntu VM" case the UX is unchanged, except that the
configuration is visible and editable on disk.

## D2. SSH-key warning — detect and warn, always on

After reading the file, before uploading, run a shallow text
check:

```go
if !bytes.Contains(content, []byte("ssh_authorized_keys:")) {
    fmt.Fprintf(opts.Stderr, "warning: cloud-init file %s has no ssh_authorized_keys; you may not be able to SSH in\n", path)
}
```

**Why warn, not error:** a password-only VM or a VM configured
via `write_files` with a key dropped elsewhere is legitimate.
The user may know what they're doing. We surface the risk and
let them decide.

**Why no silencer flag:** the previous iteration had
`--no-ssh-key-check`. Now that the file lives under the user's
control and is seeded with the key by default, the warning only
fires when the user has actively removed the key — which is
either a mistake worth flagging or a conscious choice the user
can ignore. A flag adds a maintenance surface for negligible
benefit.

**Why a string contains, not YAML parse:** YAML parsing pulls
in a dependency (`gopkg.in/yaml.v3` is already in-tree) but
introduces false negatives — a structured parse that only
checks `top.users[*].ssh_authorized_keys` misses `#cloud-config`
that uses `ssh_authorized_keys:` at the top level, or under a
nested write_files scheme. The text check is coarse but catches
the typical mistake.

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

`pmox configure` does not validate snippet support on the chosen
storage. It does, however, write the starter cloud-init file
(see D11).

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

**Why `io.Pipe` and a goroutine** — `multipart.Writer` can't
produce its boundary until it's been written to, and a
preallocated `bytes.Buffer` would hold the whole file in memory
twice. Pipe-plus-goroutine streams it. Our snippet files are
≤64 KiB so the buffer approach would be fine, but pipe is
idiomatic and avoids the double-allocation pattern.

**Rejected:** adding a dedicated `requestMultipart` helper in
`client.go`. One caller, ~30 lines; inlining into `storage.go`
keeps `client.go` focused on the two common shapes (query +
form). If a second multipart caller shows up, factor then.

## D5. Snippet filename convention

```
pmox-<vmid>-user-data.yaml
```

Frozen. This is the filename used by:
- `snippet.Upload` when writing
- `snippet.Cleanup` when deleting (slice 6's delete flow)
- any future "list pmox-owned snippets" command

Storage path as seen by PVE:
`<storage>:snippets/pmox-<vmid>-user-data.yaml`, which expands to
`/var/lib/vz/snippets/pmox-<vmid>-user-data.yaml` on default
directory storage.

**Vmid in the filename** rather than VM name because names can
collide and change; vmids can't.

**Rejected:** a hash of file contents. Harder to clean up
retroactively if something goes wrong, and the vmid is enough.

Note that this filename (on PVE) is unrelated to the local
user-facing filename (`~/.config/pmox/cloud-init/<slug>.yaml`)
from D11. PVE names snippets by target VM; the local file is
named by source server.

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
someone hits it, they can split the file into `write_files` + a
pmox-hosted http GET in `runcmd`, but that's out of scope for v1.

**UTF-8 check** catches accidentally passing a binary file. The
`utf8.Valid` from stdlib is zero-cost on small inputs.

## D7. Delete hook — best effort

```go
// cmd/pmox/delete.go, after the destroy task wait succeeds
if err := snippet.Cleanup(ctx, client, node, cicustomValue); err != nil {
    fmt.Fprintf(os.Stderr, "warning: could not remove snippet for vm %d: %v\n", vmid, err)
}
```

`Cleanup` parses `cicustomValue` (format
`user=<storage>:snippets/<filename>[,meta=...][,network=...]`),
extracts storage and filename, and calls `DeleteSnippet(ctx,
node, storage, filename)`. If the file doesn't exist, PVE
returns 404 and `DeleteSnippet` returns `ErrNotFound`, which
`Cleanup` swallows as success.

**Which storage?** `pmox delete` doesn't know at delete time
which storage the snippet lives on — the launch could have been
against a non-default `--storage`. Parsing the `cicustom` field
handles that case because the value carries the storage.

**When `cicustom` is absent:** pre-slice-7 VMs launched under
the old built-in path don't have a `cicustom` key. `pmox delete`
must still work on those. If `GetConfig` returns a config with
no `cicustom`, snippet cleanup is skipped silently. New
pmox-launched VMs will always have one.

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
- `TestValidateStorage_Missing`: storage without `snippets` →
  error message contains storage name and content list

Tests in `internal/launch/launch_test.go`:
- `TestRun_HappyPath` — asserts `PostSnippet` is called before
  `SetConfig`, and the `SetConfig` body contains
  `cicustom=user=<storage>:snippets/pmox-<vmid>-user-data.yaml`
  and does NOT contain `sshkeys=` or `ciuser=`. This replaces
  the old "built-in path" happy-path test entirely.
- `TestRun_MissingCloudInitFile` — `opts.CloudInitPath` points
  at a non-existent path; assert the launcher returns an error
  wrapping the OS not-exist, the error string mentions `pmox
  configure --regen-cloud-init`, and no PVE API call is issued.
- `TestRun_InvalidCloudInitFile` — binary bytes on disk; assert
  validation fails before any PVE call.

## D10. Example file as the configure template

`examples/cloud-init.yaml` is shipped in this slice *and* is the
authoritative template source for `pmox configure`'s generator.
Storing the template as a repo file (loaded into the binary at
build time via `go:embed`) instead of as a Go string literal
keeps it reviewable as YAML and lets the in-repo test suite
validate it using the same validators end users run.

```go
// internal/config/cloudinit.go
//go:embed cloud-init.template.yaml
var cloudInitTemplate []byte // copy of examples/cloud-init.yaml with {{.User}}/{{.SSHPubkey}} placeholders

func RenderTemplate(user, sshPubkey string) ([]byte, error) { /* text/template.Execute */ }
```

A test in `internal/snippet/snippet_test.go` reads
`examples/cloud-init.yaml` and runs `ValidateContent` + the
SSH-key text check against it, asserting no errors. This
catches accidental regressions where we break our own template.

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

A second test asserts the rendered output of `RenderTemplate`
also passes `ValidateContent` and contains the substituted
pubkey — so the template-as-shipped and the template-as-rendered
stay in sync.

## D11. Configure-time file generation

`pmox configure` generates the cloud-init file at the end of the
interactive flow, after the SSH key and default user have been
collected. Two rules:

1. **Idempotent on existing files** — if
   `~/.config/pmox/cloud-init/<slug>.yaml` already exists, leave
   it alone and print `cloud-init template already exists at
   <path> — not overwriting`. This protects user edits across
   reconfigures.
2. **Atomic write** — use a temp file + rename. A partial write
   must never leave half a template on disk.

**Server slug:** derived from the canonical URL with a pure
function:

```go
// internal/config/cloudinit.go
func Slug(canonicalURL string) (string, error) {
    u, err := url.Parse(canonicalURL)
    if err != nil { return "", err }
    host := u.Hostname()
    port := u.Port()
    if port == "" { port = "8006" }
    return fmt.Sprintf("%s-%s", host, port), nil
}
```

Example: `https://192.168.0.185:8006` → `192.168.0.185-8006`.

**File path resolver:**

```go
func CloudInitPath(canonicalURL string) (string, error) {
    slug, err := Slug(canonicalURL)
    if err != nil { return "", err }
    dir, err := os.UserConfigDir() // ~/.config
    if err != nil { return "", err }
    return filepath.Join(dir, "pmox", "cloud-init", slug+".yaml"), nil
}
```

Not stored in `config.Server` — derivable at call time from the
canonical URL, which is already the key in `cfg.Servers`.

**Generator:**

```go
func WriteStarterCloudInit(path, user, sshPubkey string) error {
    if _, err := os.Stat(path); err == nil {
        return ErrAlreadyExists // caller prints the "not overwriting" line
    }
    content, err := RenderTemplate(user, sshPubkey)
    if err != nil { return err }
    if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { return err }
    return atomicWrite(path, content, 0o600)
}
```

**`--regen-cloud-init` subflag:** regenerates the file for a
selected server without walking the full configure flow. It:
1. Loads `config.Load()`, finds the server (prompts for URL if
   more than one is configured and `--server` wasn't passed).
2. Resolves the path via `CloudInitPath(canonicalURL)`.
3. If the file exists, prompts `cloud-init file already exists
   at <path>. Overwrite? [y/N]`.
4. Calls `RenderTemplate(srv.User, srv.SSHPubkey)` and writes
   atomically.
5. Prints the resulting path.

This is the documented path for "my file is missing" and "reset
my customizations," referenced by the launch-time error message
in D12.

## D12. Missing-file error at launch time

When the launcher can't read the cloud-init file, the error is
explicit about what went wrong, where the file should be, and
how to regenerate a sane default:

```
read cloud-init file /home/e/.config/pmox/cloud-init/192.168.0.185-8006.yaml: open ...: no such file or directory

hint: run 'pmox configure --regen-cloud-init' to write a fresh default,
      or create the file manually with your own cloud-init content.
```

**Why not auto-regenerate on the fly:** a silent regenerate at
launch time loses any customizations the user previously made —
if the file went missing because of an `rm` mistake, the user
wants to know. An explicit regen step lets them decide.

**Where the error is returned from:** the very top of
`launch.Run`, before any PVE API call, so a missing file cannot
leave an orphan VM.
