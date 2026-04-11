## 1. pveclient — new write-path endpoints

- [x] 1.1 Add `CreateVM(ctx, node, vmid int, kv map[string]string) (string, error)` to `internal/pveclient/vm.go` — `POST /nodes/{node}/qemu`, form body from kv plus `vmid`, returns UPID via `parseDataString`
- [x] 1.2 Add `ConvertToTemplate(ctx, node, vmid int) error` to `internal/pveclient/vm.go` — `POST /nodes/{node}/qemu/{vmid}/template` with empty form, returns nil on 200
- [x] 1.3 Create `internal/pveclient/storage.go` (new file)
- [x] 1.4 Implement `DownloadURL(ctx, node, storage string, params map[string]string) (string, error)` — `POST /nodes/{node}/storage/{storage}/download-url`, returns UPID
- [x] 1.5 Implement `UploadSnippet(ctx, node, storage, filename string, content []byte) error` — multipart POST to `/nodes/{node}/storage/{storage}/upload` with `content=snippets`, filename field, and file part; uses a new internal helper `requestMultipart` OR constructs the body inline and calls `http.NewRequestWithContext` directly (pick whichever keeps `client.go` small)
- [x] 1.6 Implement `UpdateStorageContent(ctx, storage, content string) error` — cluster-wide `PUT /storage/{storage}`, form body `content=<value>` (comma-encoded per url.Values.Encode)
- [x] 1.7 Unit test `CreateVM` in `internal/pveclient/vm_test.go`: assert path, form fields including a kv entry with `importfrom=...` passed through unchanged, 500 wraps `ErrAPIError`, 400 surfaces PVE text
- [x] 1.8 Unit test `ConvertToTemplate`: assert path, empty body, 400 on running-VM wraps `ErrAPIError`
- [x] 1.9 Unit test `DownloadURL`: assert path, form contains url/content/filename/checksum/checksum-algorithm verbatim, UPID parsing, 403 wraps `ErrUnauthorized`
- [x] 1.10 Unit test `UploadSnippet`: assert path, Content-Type begins with `multipart/form-data; boundary=`, multipart parts include `content=snippets`, `filename=<name>`, and a file part whose bytes match the supplied content; 400 wraps `ErrAPIError`
- [x] 1.11 Unit test `UpdateStorageContent`: assert path, form body `content=iso%2Cvztmpl%2Cbackup%2Csnippets`, 403 wraps `ErrUnauthorized`
- [x] 1.12 Run `go build ./internal/pveclient/...` and `go test ./internal/pveclient/... -race`

## 2. internal/template — package scaffolding

- [x] 2.1 Create `internal/template/build.go` with `type Options struct { Client *pveclient.Client; Node, Bridge string; Wait time.Duration; Stderr io.Writer; Verbose bool; PickImage func([]ImageEntry) int; PickTargetStorage func([]pveclient.Storage) int; PickSnippetsStorage func([]pveclient.Storage) int; ConfirmEnableSnippets func(name string) bool }`
- [x] 2.2 Declare `type Result struct { VMID int; Name string; Image ImageEntry }`
- [x] 2.3 Declare `func Run(ctx context.Context, opts Options) (*Result, error)` with a `panic("TODO")` body — fill in during later tasks

## 3. internal/template — simplestreams fetcher

- [x] 3.1 Create `internal/template/simplestreams.go` with `const defaultCatalogueURL = "https://cloud-images.ubuntu.com/releases/streams/v1/com.ubuntu.cloud:released:download.json"`
- [x] 3.2 Declare `type ImageEntry struct { Release, Codename, VersionDate, URL, SHA256, Label string; IsLTS bool }`
- [x] 3.3 Implement `fetchCatalogue(ctx context.Context, url string) ([]ImageEntry, error)` — net/http GET, JSON decode, filter to amd64 disk1.img items, sort newest-first by VersionDate, cap at 10
- [x] 3.4 Implement `defaultImageIndex(entries []ImageEntry) int` — return index of latest LTS, or 0 if none
- [x] 3.5 Create `internal/template/testdata/simplestreams.json` — trimmed fixture with 2–3 releases (noble LTS, jammy LTS, a non-LTS interim) and the sha256 fields populated with dummy hashes
- [x] 3.6 Unit test `TestFetchCatalogue_FromFixture`: spin up `httptest.Server` serving the fixture, call `fetchCatalogue`, assert 2–3 entries, sort order, sha256 roundtrip, IsLTS flag
- [x] 3.7 Unit test `TestFetchCatalogue_BadJSON`: server returns `{`, assert error wraps parse failure and mentions the URL
- [x] 3.8 Unit test `TestFetchCatalogue_HTTP500`: server returns 500, assert error surfaces status code
- [x] 3.9 Unit test `TestDefaultImageIndex`: table cases covering "latest LTS cursor", "no LTS fallback to 0", "empty list returns 0"

## 4. internal/template — embedded bake snippet

- [x] 4.1 Create `internal/template/snippet.yaml` with the exact cloud-config from design D6
- [x] 4.2 Create `internal/template/cloudinit.go` with `//go:embed snippet.yaml` and `var bakeSnippet []byte`
- [x] 4.3 Declare `const bakeSnippetFilename = "pmox-qga-bake.yaml"`
- [x] 4.4 Implement `templateName(img ImageEntry, vmid int) string` → e.g. `ubuntu-2404-pmox-9000` (kebab-case, deterministic from release codename + vmid)
- [x] 4.5 Unit test `TestBakeSnippetContent`: assert snippet contains `qemu-guest-agent`, `cloud-init clean`, `truncate -s 0 /etc/machine-id`, `poweroff`
- [x] 4.6 Unit test `TestTemplateName`: table cases covering noble/jammy/focal with VMID 9000, 9050, 9099

## 5. internal/template — VMID allocator

- [x] 5.1 Create `internal/template/vmid.go` with `const (vmidRangeLo = 9000; vmidRangeHi = 9099)`
- [x] 5.2 Implement `reserveVMID(ctx context.Context, c *pveclient.Client, node string) (int, error)` — call `ListTemplates` (wait — need *all* VMs in the range, not just templates; use a new or existing method)
- [x] 5.3 If `ListTemplates` proves insufficient, add `ListAllVMIDs(ctx, node) ([]int, error)` in `internal/pveclient/nodes.go` as part of task group 1 retroactively — alternatively use `ClusterResources` which already exists
- [x] 5.4 Filter returned VMIDs to `9000..9099`, pick the lowest unused, return it
- [x] 5.5 If all 100 are occupied, return error `"vmid range 9000-9099 is full; delete unused templates first"`
- [x] 5.6 Unit test `TestReserveVMID_EmptyRange`: mock returns no VMs in range, assert 9000 returned
- [x] 5.7 Unit test `TestReserveVMID_LowestGap`: mock returns 9000, 9001, 9003, assert 9002 returned
- [x] 5.8 Unit test `TestReserveVMID_Full`: mock returns 9000–9099 fully populated, assert error contains "9000-9099 is full"

## 6. internal/template — snippets storage handler

- [x] 6.1 Create `internal/template/storage.go`
- [x] 6.2 Implement `func dirCapable(s pveclient.Storage) bool` returning true for types `dir`, `nfs`, `cifs`, `cephfs`, `glusterfs`
- [x] 6.3 Implement `func hasContent(s pveclient.Storage, content string) bool` helper
- [x] 6.4 Implement `func ensureSnippetsStorage(ctx context.Context, c *pveclient.Client, node string, confirm func(string) bool) (string, error)` — runs the three-state logic from design D4; returns the storage name to use
- [x] 6.5 Unit test `TestEnsureSnippetsStorage_AlreadyEnabled`: mock `ListStorage` returns a storage with `snippets` in content, assert returned name, assert `confirm` was not called
- [x] 6.6 Unit test `TestEnsureSnippetsStorage_PromptAccept`: mock returns dir-capable storage without snippets, `confirm` returns true, assert `UpdateStorageContent` was called with original content + snippets appended
- [x] 6.7 Unit test `TestEnsureSnippetsStorage_PromptReject`: same setup, `confirm` returns false, assert error contains "snippets storage required"
- [x] 6.8 Unit test `TestEnsureSnippetsStorage_NoDirCapable`: mock returns only LVM/ZFS storages, assert error contains "create a directory-type storage" and `confirm` was not called

## 7. internal/template — state machine wiring

- [x] 7.1 Fill in `Run(ctx, opts)` phases in order: version check → catalogue fetch → pick image → pick target storage → ensure snippets storage → upload snippet → reserve vmid → download-url + WaitTask → CreateVM + WaitTask → Start + WaitTask → poll GetStatus until stopped → SetConfig delete=ide2 → ConvertToTemplate
- [x] 7.2 Phase 0 (version): call `GetVersion`, parse major/minor with `strconv.Atoi`, error with `"PVE 8.0 or later required (found %s)"` if major < 8
- [x] 7.3 Phase 1 (catalogue): call `fetchCatalogue(ctx, defaultCatalogueURL)`, wrap error as `"fetch ubuntu catalogue: %w"`
- [x] 7.4 Phase 2 (pick image): call `opts.PickImage(entries)`, select chosen entry
- [x] 7.5 Phase 3 (pick target storage): call `ListStorage`, filter to `SupportsVMDisks()` and active+enabled, call `opts.PickTargetStorage`, error out cleanly if list is empty
- [x] 7.6 Phase 4 (snippets): call `ensureSnippetsStorage(ctx, client, node, confirm)` — where `confirm` wraps `opts.ConfirmEnableSnippets`
- [x] 7.7 Phase 5 (upload): call `UploadSnippet(ctx, node, snippetsStorage, bakeSnippetFilename, bakeSnippet)`
- [x] 7.8 Phase 6 (reserve vmid): call `reserveVMID(ctx, client, node)`
- [x] 7.9 Phase 7 (download): call `DownloadURL` with `content=iso`, `url=img.URL`, `filename=<stable>`, `checksum=img.SHA256`, `checksum-algorithm=sha256`; then `WaitTask` with 30 minute budget (downloads of 600 MB cloud images take time)
- [x] 7.10 Phase 8 (create-vm): build the kv map from design (memory, cores, cpu, net0, scsi0 with importfrom, ide2, cicustom, agent, serial0, vga, scsihw, boot, ipconfig0, name), call `CreateVM`, `WaitTask` with 5 minute budget
- [x] 7.11 Phase 9 (start): call `Start` and `WaitTask` with 2 minute budget
- [x] 7.12 Phase 10 (wait stopped): poll `GetStatus` every 5 seconds until `status=stopped`, default 10 minute budget overridable by `opts.Wait`; return wrapped timeout error naming the VMID
- [x] 7.13 Phase 11 (detach cloud-init): call `SetConfig(ctx, node, vmid, map[string]string{"delete": "ide2"})`
- [x] 7.14 Phase 12 (convert): call `ConvertToTemplate(ctx, node, vmid)`
- [x] 7.15 Every phase error wraps with the messages from design D11

## 8. internal/template — integration test

- [x] 8.1 Create `internal/template/build_test.go`
- [x] 8.2 Build a shared `fakePVE` helper using `httptest.Server` that dispatches by path and carries a stateful counter so `GetStatus` returns `running` twice then `stopped`
- [x] 8.3 `TestRun_HappyPath`: set `Options.PickImage` / `PickTargetStorage` / `PickSnippetsStorage` to fixed-choice functions, `ConfirmEnableSnippets` to return false (since fixture already has snippets), assert `*Result` VMID is 9000 and the endpoint sequence matches design D2
- [x] 8.4 `TestRun_PVE7xRejected`: mock `/version` returns `{"data":{"version":"7.4"}}`, assert error contains `PVE 8.0 or later required`
- [x] 8.5 `TestRun_DownloadFailurePre_VMCreated`: mock download task fails, assert no POST to `/qemu` was made and returned error wraps the download failure
- [x] 8.6 `TestRun_WaitStoppedTimeout`: mock always returns `running`, pass `Options.Wait=2*time.Second`, assert error wraps `ErrTimeout` and mentions the VMID
- [x] 8.7 `TestRun_ConvertFailureLeavesVM`: mock returns 500 on `ConvertToTemplate`, assert error names VMID and mentions cleanup, assert no `DELETE` calls were recorded on the fake server

## 9. cmd/pmox/create_template.go — Cobra wiring

- [x] 9.1 Create `cmd/pmox/create_template.go` with `func newCreateTemplateCmd() *cobra.Command`
- [x] 9.2 Register in `cmd/pmox/main.go` via `rootCmd.AddCommand(newCreateTemplateCmd())`
- [x] 9.3 Flags: `--node <name>` (persistent), `--bridge <name>`, `--wait <duration>` (default 10m)
- [x] 9.4 `RunE`: resolve server via `server.Resolve`, build `pveclient.Client`, enforce TTY on stdin (error with `ExitConfig` and "interactive TTY required" if not a TTY), build `template.Options` wiring interactive pickers to `internal/tui.SelectOne`
- [x] 9.5 Verbose log line: when `-v` is set, write `using server <url> (<reason>)` to stderr before any API call, matching the `launch` command
- [x] 9.6 On success, print to stdout: `created template <name> (vmid=<id>); launch with: pmox launch <name> --template <id>`
- [x] 9.7 On error, map via `exitcode.From` and print the wrapped error to stderr
- [x] 9.8 Wire `ConfirmEnableSnippets` to a small stdin-reader that accepts `y`/`Y`, treats anything else as no

## 10. cmd/pmox — tests

- [x] 10.1 Create `cmd/pmox/create_template_test.go`
- [x] 10.2 `TestCreateTemplate_NonTTYRejected`: run with a bytes.Buffer stdin (no tty), assert exit code `ExitConfig` and stderr contains "interactive TTY required"
- [x] 10.3 `TestCreateTemplate_VerboseLogLine`: capture stderr, assert the server-resolution log line appears exactly once before any API call (short-circuit via an injected fake that returns an error on the first call)
- [x] 10.4 `TestCreateTemplate_FlagDefaults`: assert `--wait` defaults to 10m and parses `--wait 5m` correctly

## 11. README and ROADMAP updates

- [x] 11.1 Add a row to the README permissions table: `Datastore.Allocate` on `/storage/{id}` — needed for the snippets-enable PUT
- [x] 11.2 Add a paragraph to the README under a new `## Creating a template` section explaining `pmox create-template` at a 5-sentence level, pointing at the slice spec for details
- [x] 11.3 Update `ROADMAP.md`: add slice 10 `create-template` to the status table (state: Shipped once this lands) and add a description block under "Shipped (continued)"
- [x] 11.4 Verify PVE token permissions for `download-url`: research whether `Sys.Modify` on `/` is required (it may not be, depending on PVE version); update the README row accordingly before shipping

## 12. Verification

- [x] 12.1 `go build ./...` passes
- [x] 12.2 `go test ./internal/template/... -race` passes
- [x] 12.3 `go test ./... -race` passes (full suite)
- [x] 12.4 `make lint` passes
- [x] 12.5 `pmox create-template --help` shows `--node`, `--bridge`, `--wait` with one-line descriptions
- [x] 12.6 `pmox --help` lists `create-template` in the command table
- [ ] 12.7 Manual smoke test against a real PVE 8.x cluster: `pmox create-template` picks Ubuntu 24.04, completes in <6 minutes on a typical uplink, produces a template in the 9000–9099 range, and `pmox launch --template <id>` successfully clones from it
- [ ] 12.8 Manual smoke test on PVE 7.x: assert the command exits cleanly with the version-check error message
