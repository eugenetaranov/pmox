## 0. Direction change — amendment notes

This slice was originally scoped as an opt-in `--cloud-init`
flag layered over the existing built-in cloud-init path. It has
been re-scoped to: (a) collapse to a single code path, (b)
generate the cloud-init file at `pmox configure` time, (c)
remove the built-in `ciuser`/`sshkeys` branch entirely.

Tasks below reflect the final target shape. Items marked `[x]`
are already implemented against the earlier design and still
apply. Items marked `[ ]` are net-new or superseding work.
Items marked `[~]` were done under the old design and need to
be reverted or modified.

## 1. pveclient — storage endpoints

- [x] 1.1 Create `internal/pveclient/storage.go`
- [x] 1.2 Declare `type StorageContent struct { Volid, Format string; Size int64 }` with json tags
- [x] 1.3 `func (c *Client) PostSnippet(ctx context.Context, node, storage, filename string, content []byte) error` — multipart upload via `io.Pipe` + `mime/multipart` per design D4
- [x] 1.4 Goroutine writes `content` field (value `"snippets"`), `filename` field, and `file` form file; closes pipe on exit
- [x] 1.5 Set headers: `Authorization`, `Accept: application/json`, `Content-Type: <mw.FormDataContentType()>`
- [x] 1.6 Status-code handling mirrors `requestForm`: 401 → `ErrUnauthorized`, 404 → `ErrNotFound`, 5xx → `ErrAPIError`
- [x] 1.7 `func (c *Client) DeleteSnippet(ctx context.Context, node, storage, filename string) error` — DELETE via `request()`; 404 → `ErrNotFound`
- [x] 1.8 `func (c *Client) ListStorageContent(ctx context.Context, node, storage, contentFilter string) ([]StorageContent, error)` — GET with query `?content=<filter>`

## 2. pveclient — storage tests

- [x] 2.1 Create `internal/pveclient/storage_test.go`
- [x] 2.2 `TestPostSnippet_HappyPath`
- [x] 2.3 `TestPostSnippet_ServerError`
- [x] 2.4 `TestDeleteSnippet_HappyPath`
- [x] 2.5 `TestDeleteSnippet_NotFound`
- [x] 2.6 `TestListStorageContent` with fixture

## 3. internal/snippet — validation

- [x] 3.1 Create `internal/snippet/snippet.go`
- [x] 3.2 Constant `MaxBytes = 64 * 1024`
- [x] 3.3 `func ValidateContent(content []byte) error` — empty, oversized, non-UTF-8 checks
- [x] 3.4 `func HasSSHKeys(content []byte) bool`
- [x] 3.5 `func Filename(vmid int) string` — `pmox-<vmid>-user-data.yaml`
- [x] 3.6 Table tests for each validator

## 4. internal/snippet — storage validation

- [x] 4.1 `func ValidateStorage(ctx context.Context, client *pveclient.Client, node, storage string) error`
- [x] 4.2 Uses `client.ListStorage(ctx, node)`
- [x] 4.3 Finds matching entry; error if not found
- [x] 4.4 Parses `Content` field; error if missing `snippets`
- [x] 4.5 Error message matches design D3 template
- [x] 4.6 Tests for happy-path, missing-snippets, unknown-storage

## 5. internal/snippet — upload orchestration

- [x] 5.1 `func Upload(ctx, client, node, storage, vmid, content) error`
- [x] 5.2 Calls `ValidateStorage`, `ValidateContent`, `PostSnippet`
- [x] 5.3 `func Cleanup(ctx, client, node, cicustomValue) error`
- [x] 5.4 Parses `user=<storage>:snippets/<filename>[,meta=...][,network=...]`
- [x] 5.5 Split logic for storage and filename extraction
- [x] 5.6 Calls `DeleteSnippet`; swallows `ErrNotFound`
- [x] 5.7 Returns other errors unchanged
- [x] 5.8 `ParseCicustom(value string) (storage, filename string, err error)` — exported
- [x] 5.9 `TestParseCicustom` table cases

## 6. internal/config — cloud-init resolver and template

- [x] 6.1 Create `internal/config/cloudinit.go`
- [x] 6.2 `func Slug(canonicalURL string) (string, error)` — pure function; `<host>-<port>` with default port `8006` when absent
- [x] 6.3 `func CloudInitPath(canonicalURL string) (string, error)` — resolves to `<UserConfigDir>/pmox/cloud-init/<slug>.yaml`
- [x] 6.4 Embed `internal/config/cloud-init.template.yaml` via `//go:embed`
- [x] 6.5 `func RenderTemplate(user, sshPubkey string) ([]byte, error)` — `text/template.Execute`
- [x] 6.6 `var ErrCloudInitExists = errors.New("cloud-init template already exists")` (renamed from ErrAlreadyExists for clarity)
- [x] 6.7 `func WriteStarterCloudInit(path, user, sshPubkey string) error` — returns `ErrCloudInitExists` if file present; else `MkdirAll(0o700)` + atomic temp-file write with `0o600`. Plus `WriteCloudInit` that unconditionally overwrites (used by `--regen-cloud-init`).
- [x] 6.8 Tests: `TestSlug`; `TestCloudInitPath`; `TestRenderTemplate`; `TestWriteStarterCloudInit_FirstWrite`, `_Idempotent`, `TestWriteCloudInit_Overwrites`, `TestTemplateMatchesExample`

## 7. internal/launch — single-path refactor

- [x] 7.1 `launch.Options` has `CloudInitPath string`. Keep.
- [x] 7.2 `NoSSHKeyCheck` field removed from `launch.Options`
- [x] 7.3 `launch.Run` reads `opts.CloudInitPath` upfront; missing file error contains the regen hint
- [x] 7.4 Other read errors wrapped `fmt.Errorf("read cloud-init file: %w", err)`
- [x] 7.5 `ValidateContent` + `ValidateStorage` run before any PVE API call
- [x] 7.6 SSH-key warning always on — no `NoSSHKeyCheck` guard
- [x] 7.7 Config phase unconditionally calls `PostSnippet` → `BuildCustomKV` → `SetConfig`
- [x] 7.8 Opt-in `if opts.CloudInitPath == ""` branch deleted
- [x] 7.9 `BuildBuiltinKV` and its tests deleted
- [x] 7.10 `BuildCustomKV(opts Options, vmid int) map[string]string` — returns `{name, memory, cores, agent, ipconfig0, ide2, cicustom}`
- [x] 7.11 `cicustom` value is `fmt.Sprintf("user=%s:snippets/%s", opts.Storage, snippet.Filename(vmid))`
- [x] 7.12 `TestRun_HappyPath` asserts upload precedes full SetConfig, body has `cicustom` and lacks `ciuser`/`sshkeys`; built-in path tests gone
- [x] 7.13 `TestRun_MissingCloudInitFile` — error contains `pmox configure --regen-cloud-init`; no PVE call issued
- [x] 7.14 `TestRun_InvalidCloudInitFile` — binary file wraps validation error; no PVE call issued

## 8. cmd/pmox/launch and clone — remove opt-in flags

- [x] 8.1 `--cloud-init <path>` flag removed from `cmd/pmox/launch.go`
- [x] 8.2 `--no-ssh-key-check` flag removed from `cmd/pmox/launch.go`
- [x] 8.3 Same flags removed from `cmd/pmox/clone.go`
- [x] 8.4 Both commands populate `launch.Options.CloudInitPath` from `config.CloudInitPath(resolved.URL)`
- [x] 8.5 `launch` and `clone` Long help now describes the per-server cloud-init file and points at `pmox configure --regen-cloud-init`

## 9. cmd/pmox/configure — generation and `--regen-cloud-init`

- [x] 9.1 `runInteractive` calls `writeInitialCloudInit` at the end, which invokes `config.WriteStarterCloudInit(path, user, pubkeyContent)`
- [x] 9.2 On `ErrCloudInitExists`, prints `cloud-init template already exists at <path> — not overwriting`
- [x] 9.3 On success, prints `wrote cloud-init template to <path> — edit it to customize packages, users, runcmd`
- [x] 9.4 Any other error prints a warning to stderr but never returns it — credentials are already saved
- [x] 9.5 `--regen-cloud-init` bool flag added, mutually exclusive with `--list` and `--remove`
- [x] 9.6 `runRegenCloudInit(ctx, prompter)` loads config, picks the single configured server or prompts via `tui.SelectOne`, reads the stored pubkey, prompts for overwrite if file exists, writes atomically
- [x] 9.7 `TestWriteInitialCloudInit_FirstWrite` — asserts file written with substituted content
- [x] 9.8 `TestWriteInitialCloudInit_DoesNotOverwrite` — pre-existing file unchanged, "not overwriting" printed
- [x] 9.9 `TestRegenCloudInit_Overwrite` — existing file + "y" → rewritten
- [x] 9.10 `TestRegenCloudInit_Abort` — existing file + "n" → unchanged
- [x] 9.11 `TestRegenCloudInit_MissingFile` — no file → written without prompt

## 10. cmd/pmox/delete — snippet cleanup

- [x] 10.1 After successful destroy WaitTask, if pre-destroy `GetConfig` had a `cicustom` key, call `snippet.Cleanup(ctx, client, node, cicustomValue)`
- [x] 10.2 `GetConfig` called before the destroy call
- [x] 10.3 Cleanup errors (non-ErrNotFound) print `warning: could not remove snippet for vm %d: %v` to stderr; command exits 0
- [x] 10.4 Parse error on `cicustom` value prints warning, proceeds
- [x] 10.5 `delete_test.go`: custom-cloud-init path asserts `DeleteSnippet` called; legacy path asserts it is not
- [x] 10.6 Legacy path test retained — pre-slice VMs without `cicustom` still exist in the wild, so `delete_test.go`'s "no cicustom" case still guards the skip-cleanup branch

## 11. examples/cloud-init.yaml and embedded template

- [x] 11.1 `examples/cloud-init.yaml` exists with `#cloud-config`, `users:` block with `name: ubuntu`, `sudo: ALL=(ALL) NOPASSWD:ALL`, `ssh_authorized_keys:` placeholder, `package_update: true`, `packages: [qemu-guest-agent]`, `runcmd: [systemctl enable --now qemu-guest-agent]`
- [x] 11.2 File size well under 64 KiB; valid UTF-8
- [x] 11.3 `TestExampleFileIsValid` in `internal/snippet/snippet_test.go`
- [x] 11.4 `internal/config/cloud-init.template.yaml` created with `{{.User}}` / `{{.SSHPubkey}}` placeholders
- [x] 11.5 `TestTemplateMatchesExample` in `internal/config/cloudinit_test.go` — asserts both pass `ValidateContent` and share the same required stanzas (loosened from strict byte-equivalence since the template carries a "generated by" header the example doesn't need)

## 12. Verification

- [x] 12.1 `go build ./...` passes
- [x] 12.2 `go test ./... -race` passes
- [~] 12.3 `make lint` passes — the only remaining errors (`cmd/pmox/mount.go` errcheck + unused) are pre-existing on `main` and unrelated to this change
- [x] 12.4 `pmox launch --help` no longer shows `--cloud-init` or `--no-ssh-key-check`
- [x] 12.5 `pmox configure --help` shows `--regen-cloud-init`
- [ ] 12.6 Manual: fresh configure writes a starter file at the expected path
- [ ] 12.7 Manual: launching a VM after configure uses the generated file; VM boots with SSH working and `qemu-guest-agent` installed
- [ ] 12.8 Manual: edit the generated file to add an extra package, relaunch, assert the package is present
- [ ] 12.9 Manual: `rm` the generated file, run `pmox launch`, assert the error names the path and suggests `pmox configure --regen-cloud-init`
- [ ] 12.10 Manual: `pmox configure --regen-cloud-init` rewrites the file with overwrite confirmation
- [ ] 12.11 Manual: `pmox delete` removes the snippet file from the PVE host
- [ ] 12.12 Manual: launch with a storage that lacks `snippets` content type; verify the error message matches the D3 template
