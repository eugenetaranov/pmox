## 1. Config — `SnippetStorage` field

- [x] 1.1 Add `SnippetStorage string \`yaml:"snippet_storage,omitempty"\`` to `Server` in `internal/config/config.go`
- [x] 1.2 Round-trip test: load a YAML with `snippet_storage: local` and assert the field is populated; save it back and assert the marshal preserves the key
- [x] 1.3 Back-compat test: load a pre-slice YAML with no `snippet_storage` and assert parsing succeeds with empty string

## 2. pveclient — `UpdateStorageContent`

- [x] 2.1 Add `func (c *Client) UpdateStorageContent(ctx context.Context, storage string, content []string) error` to `internal/pveclient/storage.go`
- [x] 2.2 Implementation: PUT `/storage/{storage}` with form body `content=<comma-joined>`; reuse `requestForm`
- [x] 2.3 Status-code handling mirrors other methods: 401 → `ErrUnauthorized`, 404 → `ErrNotFound`, 5xx → `ErrAPIError`
- [x] 2.4 Test: happy path asserts request URL, method, and form body
- [x] 2.5 Test: 404 returns `ErrNotFound`

## 3. configure — snippet-storage selection and enable flow

- [x] 3.1 New helper `pickSnippetStorage(ctx, p, client, node) string` in `cmd/pmox/configure.go`
- [x] 3.2 Enumerate `ListStorage`, keep entries whose `Content` split-and-trimmed contains `"snippets"`
- [x] 3.3 If exactly one match → return it silently (no prompt)
- [x] 3.4 If multiple matches → `tui.SelectOne("Snippet storage", opts, first)`
- [x] 3.5 If zero matches → call `offerEnableSnippets(ctx, p, client, pools)`
- [x] 3.6 `offerEnableSnippets` filters `pools` to snippet-capable backends (`dir`, `nfs`, `cifs`, `cephfs`), defaults to `local` when present, prompts `enable snippets on "<name>"? [Y/n]`
- [x] 3.7 On confirmation, compute the new content list (`existing + ",snippets"` if not already present), call `client.UpdateStorageContent(ctx, name, list)`, return the name
- [x] 3.8 On decline or zero candidates, print the manual remediation (naming `/etc/pve/storage.cfg` and pointing at `pmox configure` to re-run) and return empty string
- [x] 3.9 Wire `pickSnippetStorage` into `runInteractive` right after `pickStorage`; assign to `srv.SnippetStorage` in the saved config
- [x] 3.10 Tests for `pickSnippetStorage`: one-match auto, multi-match picker, zero-match enable-yes, zero-match enable-no (via prompter stub + fake client)

## 4. launch/clone CLI — `--snippet-storage` flag

- [x] 4.1 Add `snippetStorage string` to the flags struct in `cmd/pmox/launch.go` and register `cmd.Flags().StringVar(&f.snippetStorage, "snippet-storage", "", "storage pool for the cloud-init snippet (falls back to configured snippet_storage, then storage)")`
- [x] 4.2 Resolve `snippetStorage := firstNonEmpty(f.snippetStorage, srv.SnippetStorage, storage)` where `storage` is the already-resolved disk storage
- [x] 4.3 When the fallback to `storage` kicks in (no flag + no config), print the fallback warning to `cmd.ErrOrStderr()` once
- [x] 4.4 Mirror all of the above into `cmd/pmox/clone.go`
- [x] 4.5 Pass `SnippetStorage: snippetStorage` into `launch.Options`

## 5. launch internals — use `SnippetStorage`

- [x] 5.1 Add `SnippetStorage string` to `launch.Options` in `internal/launch/launch.go`
- [x] 5.2 Change the `ValidateStorage` call to target `opts.SnippetStorage`
- [x] 5.3 Change the `PostSnippet` call to target `opts.SnippetStorage`
- [x] 5.4 Update `BuildCustomKV` in `internal/launch/cloudinit.go` so `cicustom` uses `opts.SnippetStorage`
- [x] 5.5 Existing disk-storage validation (`SupportsVMDisks`, etc.) is unchanged and keeps running against `opts.Storage`
- [x] 5.6 Update `TestRun_HappyPath`, `TestRun_MissingCloudInitFile`, `TestRun_InvalidCloudInitFile`, and the cloudinit KV tests to populate `SnippetStorage` alongside `Storage`

## 6. delete — no functional change

- [x] 6.1 Confirm `snippet.Cleanup` still parses the storage out of the `cicustom` value (it does — `ParseCicustom` already returns `(storage, filename)`)
- [x] 6.2 Add a regression test: VM with `cicustom=user=local:snippets/pmox-104-user-data.yaml` and `storage=vm-data` still routes `DeleteSnippet` to `local`

## 7. Docs + help text

- [x] 7.1 Update `pmox launch` and `pmox clone` Long help to document `--snippet-storage` and how it relates to `--storage`
- [x] 7.2 Update `pmox configure` Long help to mention that it now picks (or enables) a snippet storage separately from the disk storage
- [x] 7.3 Update `README.md` and `openspec/specs/cloud-init-custom` (or equivalent) once this change is archived

## 9. Launch snippet upload via SFTP (PVE upload endpoint rejects `snippets`)

- [x] 9.1 Add `UploadSnippet func(ctx context.Context, storagePath, filename string, content []byte) error` callback to `launch.Options`
- [x] 9.2 In `launch.Run`, replace the `client.PostSnippet(...)` call with: `storagePath, err := opts.Client.GetStoragePath(ctx, opts.SnippetStorage)` followed by `opts.UploadSnippet(ctx, storagePath, snippet.Filename(vmid), cloudInitBytes)`
- [x] 9.3 Fail-fast in `launch.Run` (after `Phase 0`, before clone) when `opts.UploadSnippet == nil` with "no UploadSnippet injected" — programming bug guard
- [x] 9.4 In `cmd/pmox/launch.go` `runLaunch`: require `resolved.HasNodeSSH()`; if missing, return `"%w: launch needs SSH access to the Proxmox node (for snippet upload). Run 'pmox configure' to add SSH credentials.", exitcode.ErrUserInput` before calling `resolveLaunchOptions`
- [x] 9.5 In `cmd/pmox/launch.go`: lazily dial `pvessh` (mirror `cmd/pmox/create_template.go:100-115`), inject the upload closure into `launch.Options.UploadSnippet`, defer `Close`
- [x] 9.6 Mirror 9.4 + 9.5 in `cmd/pmox/clone.go` `runClone`
- [x] 9.7 Delete `pveclient.PostSnippet` and its tests (`TestPostSnippet_HappyPath`, `TestPostSnippet_ServerError`)
- [x] 9.8 Update `internal/launch/launch_test.go`: provide an in-memory `UploadSnippet` stub via `baseOpts`; assert it is called with the right `storagePath`/`filename`/`content`
- [x] 9.9 Update `cmd/pmox/clone_test.go` to set `UploadSnippet` on the partial `launch.Options` (in-memory stub)
- [x] 9.10 Add `GetStoragePath` to `pvetest` fakes used by launch and clone tests so the new resolution call succeeds
- [x] 9.11 Build, test, lint

## 8. Verification

- [x] 8.1 `go build ./...` passes
- [x] 8.2 `go test ./... -race` passes
- [x] 8.3 `make lint` passes (ignoring the pre-existing `cmd/pmox/mount.go` lint notes)
- [ ] 8.4 Manual: fresh `pmox configure` against a cluster with `local` + `vm-data` picks `vm-data` for disks, `local` for snippets
- [ ] 8.5 Manual: fresh `pmox configure` against a cluster whose only dir storage has `content=iso,vztmpl` — configure offers to enable snippets on it, applies the change, and the subsequent launch succeeds
- [ ] 8.6 Manual: `pmox launch --snippet-storage local` overrides a misconfigured `server.SnippetStorage`
- [ ] 8.7 Manual: load an old config with no `snippet_storage`, run `pmox launch`, observe the fallback warning and a successful launch when `storage` happens to also support snippets
- [ ] 8.8 Manual: `pmox delete` cleans up the snippet from the correct storage (snippet storage, not disk storage)
