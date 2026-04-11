## 1. pveclient — storage endpoints

- [ ] 1.1 Create `internal/pveclient/storage.go`
- [ ] 1.2 Declare `type StorageContent struct { Volid, Format string; Size int64 }` with json tags
- [ ] 1.3 `func (c *Client) PostSnippet(ctx context.Context, node, storage, filename string, content []byte) error` — multipart upload via `io.Pipe` + `mime/multipart` per design D4
- [ ] 1.4 Goroutine writes `content` field (value `"snippets"`), `filename` field, and `file` form file; closes pipe on exit
- [ ] 1.5 Set headers: `Authorization`, `Accept: application/json`, `Content-Type: <mw.FormDataContentType()>`
- [ ] 1.6 Status-code handling mirrors `requestForm`: 401 → `ErrUnauthorized`, 404 → `ErrNotFound`, 5xx → `ErrAPIError`
- [ ] 1.7 `func (c *Client) DeleteSnippet(ctx context.Context, node, storage, filename string) error` — DELETE via `request()`; 404 → `ErrNotFound`
- [ ] 1.8 `func (c *Client) ListStorageContent(ctx context.Context, node, storage, contentFilter string) ([]StorageContent, error)` — GET with query `?content=<filter>`

## 2. pveclient — storage tests

- [ ] 2.1 Create `internal/pveclient/storage_test.go`
- [ ] 2.2 `TestPostSnippet_HappyPath`: fake server parses `r.ParseMultipartForm(1<<20)`, asserts `content=snippets`, asserts `filename` field, asserts `file` part body equals the passed bytes
- [ ] 2.3 `TestPostSnippet_ServerError`: 500 → `ErrAPIError`
- [ ] 2.4 `TestDeleteSnippet_HappyPath`: DELETE on the full content URL returns 200
- [ ] 2.5 `TestDeleteSnippet_NotFound`: 404 → `ErrNotFound`
- [ ] 2.6 `TestListStorageContent`: fixture `testdata/storage_content_snippets.json` with 2 entries, assert parse

## 3. internal/snippet — validation

- [ ] 3.1 Create `internal/snippet/snippet.go`
- [ ] 3.2 Constant `MaxBytes = 64 * 1024`
- [ ] 3.3 `func ValidateContent(content []byte) error` — empty, oversized, non-UTF-8 checks per design D6
- [ ] 3.4 `func HasSSHKeys(content []byte) bool` — `bytes.Contains(content, []byte("ssh_authorized_keys:"))`
- [ ] 3.5 `func Filename(vmid int) string` — returns `fmt.Sprintf("pmox-%d-user-data.yaml", vmid)`
- [ ] 3.6 Test `snippet_test.go`: table cases for each validator (empty, 100 KiB, binary, good, with ssh keys, without ssh keys)

## 4. internal/snippet — storage validation

- [ ] 4.1 `func ValidateStorage(ctx context.Context, client *pveclient.Client, node, storage string) error`
- [ ] 4.2 Call `client.ListStorage(ctx, node)` (already exists from slice 2)
- [ ] 4.3 Find matching entry; error if not found
- [ ] 4.4 Parse its `Content` field (comma-separated string); error if it lacks `snippets`
- [ ] 4.5 Error message matches the exact template from design D3 — storage name, current content list, fix options, wiki link
- [ ] 4.6 Test `TestValidateStorage_HappyPath`, `TestValidateStorage_Missing`, `TestValidateStorage_UnknownStorage`

## 5. internal/snippet — upload orchestration

- [ ] 5.1 `func Upload(ctx context.Context, client *pveclient.Client, node, storage string, vmid int, content []byte) error`
- [ ] 5.2 Calls `ValidateStorage`, then `ValidateContent`, then `client.PostSnippet` with the filename from `Filename(vmid)`
- [ ] 5.3 `func Cleanup(ctx context.Context, client *pveclient.Client, node, cicustomValue string) error`
- [ ] 5.4 `Cleanup` parses `cicustomValue` — expected format `user=<storage>:snippets/<filename>[,meta=...][,network=...]`
- [ ] 5.5 Split on `,`, find the `user=` segment, further split on `:` to get storage, then strip `snippets/` prefix from filename
- [ ] 5.6 Call `DeleteSnippet(node, storage, filename)`; swallow `ErrNotFound` as success
- [ ] 5.7 Return any other error unchanged; caller decides whether to warn or fail
- [ ] 5.8 `ParseCicustom(value string) (storage, filename string, err error)` — pure function, exported for testability
- [ ] 5.9 Test `TestParseCicustom` table cases: valid, malformed (no `user=`), missing `:`, missing `snippets/` prefix

## 6. internal/launch — custom cloud-init branch

- [ ] 6.1 Extend `launch.Options` with `CloudInitPath string` and `NoSSHKeyCheck bool`
- [ ] 6.2 In `launch.Run`, before the state machine, if `CloudInitPath != ""`: read file, `ValidateContent`, `ValidateStorage`, warn-if-no-ssh (unless `NoSSHKeyCheck`)
- [ ] 6.3 File read error returns `fmt.Errorf("read cloud-init file: %w", err)` before any PVE call
- [ ] 6.4 Validation errors return wrapped before any PVE call (no orphan VM)
- [ ] 6.5 SSH warning emitted to `opts.Stderr`, not stdout
- [ ] 6.6 After clone+tag+resize, in the config phase, branch on `opts.CloudInitPath`:
- [ ] 6.7   If set: call `snippet.Upload(ctx, client, node, opts.Storage, vmid, content)`, build kv via `cloudinit.BuildCustomKV(opts, vmid)`, call `SetConfig(kv)`
- [ ] 6.8   If empty: existing built-in path via `BuildBuiltinKV` — unchanged
- [ ] 6.9 Add `func BuildCustomKV(opts Options, vmid int) map[string]string` in `cloudinit.go` — returns `{name, memory, cores, agent, ipconfig0, cicustom}` only
- [ ] 6.10 `cicustom` value is `fmt.Sprintf("user=%s:snippets/%s", opts.Storage, snippet.Filename(vmid))`
- [ ] 6.11 Tests: `TestBuildCustomKV_NoSSHKeys`: assert returned map has no `ciuser` or `sshkeys`
- [ ] 6.12 Tests: `TestRun_CustomCloudInit_HappyPath` in `launch_test.go` — fake PVE asserts multipart upload happens before SetConfig, SetConfig body has `cicustom=...` and no `ciuser`
- [ ] 6.13 Tests: `TestRun_CustomCloudInit_InvalidFile` — pass a path to a binary file, assert no PVE calls issued

## 7. cmd/pmox — launch/clone flags

- [ ] 7.1 Add `--cloud-init <path>` flag to `cmd/pmox/launch.go` via `cmd.Flags().String`
- [ ] 7.2 Add `--no-ssh-key-check` flag (bool)
- [ ] 7.3 Same flags on `cmd/pmox/clone.go`
- [ ] 7.4 Pass through to `launch.Options` fields
- [ ] 7.5 `launch --help` and `clone --help` show both flags with one-line descriptions

## 8. cmd/pmox/delete — snippet cleanup

- [ ] 8.1 After successful destroy WaitTask in `delete.go`, if the pre-destroy `GetConfig` had a `cicustom` key, call `snippet.Cleanup(ctx, client, node, cicustomValue)`
- [ ] 8.2 `GetConfig` must be called before the destroy call (VM disappears) — fetch it once up-front and cache
- [ ] 8.3 Cleanup errors (non-ErrNotFound) print `warning: could not remove snippet for vm %d: %v` to stderr and return nil from the command
- [ ] 8.4 Parse error on `cicustom` value prints a warning but proceeds
- [ ] 8.5 Test `delete_test.go` additions: custom-cloud-init VM path asserts DeleteSnippet is called; built-in VM path asserts it is not

## 9. examples/cloud-init.yaml

- [ ] 9.1 Create `examples/cloud-init.yaml` — minimal working snippet
- [ ] 9.2 Content: `#cloud-config`, `users:` block with `name: ubuntu`, `sudo: ALL=(ALL) NOPASSWD:ALL`, `ssh_authorized_keys:` with a placeholder `ssh-ed25519 AAAA... replace-with-your-key`, `package_update: true`, `packages: [qemu-guest-agent]`, `runcmd: [systemctl enable --now qemu-guest-agent]`
- [ ] 9.3 File size well under 64 KiB; valid UTF-8
- [ ] 9.4 Test `TestExampleFileIsValid` in `internal/snippet/snippet_test.go` — reads `../../examples/cloud-init.yaml`, runs `ValidateContent`, asserts `HasSSHKeys` is true, asserts file contains `qemu-guest-agent`

## 10. Verification

- [ ] 10.1 `go build ./...` passes
- [ ] 10.2 `go test ./... -race` passes
- [ ] 10.3 `make lint` passes
- [ ] 10.4 `pmox launch --help` shows `--cloud-init` and `--no-ssh-key-check`
- [ ] 10.5 Manual smoke against real cluster with a custom cloud-init file that installs an extra package; verify the package is installed on the launched VM; verify `pmox delete` removes the snippet file
- [ ] 10.6 Manual smoke: attempt to launch with a storage that does not have `snippets` in its content; verify the error message matches the D3 template exactly
