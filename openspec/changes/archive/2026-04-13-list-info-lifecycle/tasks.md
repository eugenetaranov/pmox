## 1. pveclient extensions

- [x] 1.1 Add `internal/pveclient/cluster.go` with `type Resource struct { Name, Node, Status, Tags string; VMID int }` and json tags
- [x] 1.2 `func (c *Client) ClusterResources(ctx context.Context, typeFilter string) ([]Resource, error)` — GET `/cluster/resources?type=<filter>` via `request()`
- [x] 1.3 Parse response `{"data": [...]}` into `[]Resource`
- [x] 1.4 Test `cluster_test.go`: fixture with 3 entries (2 VMs tagged `pmox`, 1 untagged), assert parsing
- [x] 1.5 Add `func (c *Client) GetConfig(ctx context.Context, node string, vmid int) (map[string]string, error)` in `vm.go`
- [x] 1.6 GetConfig parsing: response is `{"data": {"cores": 2, "memory": 2048, "net0": "virtio=..."}}` — values may be int or string. Convert everything to string via `fmt.Sprintf("%v", v)`
- [x] 1.7 Test `TestGetConfig`: fixture with mixed int/string values, assert all keys present and stringified
- [x] 1.8 Add `func (c *Client) Shutdown(ctx context.Context, node string, vmid int) (string, error)` in `vm.go` — POST `/status/shutdown`, parse UPID
- [x] 1.9 Add `func (c *Client) Stop(ctx context.Context, node string, vmid int) (string, error)` in `vm.go` — POST `/status/stop`, parse UPID
- [x] 1.10 Test `TestShutdown`, `TestStop`: mock server asserts method and path, returns UPID

## 2. internal/vm — shared resolver

- [x] 2.1 Create `internal/vm/resolve.go` with `type Ref struct { VMID int; Node, Name string }`
- [x] 2.2 `func Resolve(ctx context.Context, c *pveclient.Client, arg string) (*Ref, error)`
- [x] 2.3 Numeric path: if `strconv.Atoi(arg)` succeeds, call `ClusterResources(ctx, "vm")` and filter by VMID; one match → return, zero → `not found: vmid %d`
- [x] 2.4 Name path: call `ClusterResources(ctx, "vm")`, filter by `r.Name == arg`
- [x] 2.5 Zero matches by name: return error `VM %q not found`
- [x] 2.6 Two+ matches by name: return error `multiple VMs named %q: vmids %v — pass the VMID instead`, with VMIDs sorted ascending for stable output
- [x] 2.7 Test `resolve_test.go` with table cases per scenarios in spec.md

## 3. internal/vm — tag check

- [x] 3.1 Add `func HasPMOXTag(tagsRaw string) bool` in `internal/vm/resolve.go` — splits on `;` and `,`, case-insensitive match for `pmox`
- [x] 3.2 Test table: `""`, `"pmox"`, `"foo;pmox"`, `"pmox;bar"`, `"PMOX"`, `"notpmox"`, `"pmoxish"` — only the first four (and `PMOX`) return true
- [x] 3.3 Document with comment: PVE's tag format varies between `;` and `,` separated across versions — handle both

## 4. internal/launch — export PickIPv4

- [x] 4.1 Rename `pickIPv4` → `PickIPv4` in `internal/launch/ip.go`
- [x] 4.2 Update in-package test calls to use the exported name
- [x] 4.3 Godoc comment: "PickIPv4 implements the D-T3 heuristic shared between launch and list."

## 5. list command

- [x] 5.1 Create `cmd/pmox/list.go` with `newListCmd() *cobra.Command`
- [x] 5.2 Flags: `--all` (bool), output is handled via root persistent `--output`
- [x] 5.3 Register in `cmd/pmox/main.go` via `rootCmd.AddCommand(newListCmd())`
- [x] 5.4 RunE: resolve server, call `ClusterResources(ctx, "vm")`, client-side filter by `HasPMOXTag` unless `--all`
- [x] 5.5 For each running VM, fetch IP via `AgentNetwork` in an `errgroup.WithContext` with `SetLimit(8)`
- [x] 5.6 Render table or JSON based on `--output`; table path lives in `internal/vm/table.go`
- [x] 5.7 Create `internal/vm/table.go` with `func RenderTable(w io.Writer, rows []Row)` using `fmt.Fprintf` and width computation
- [x] 5.8 `type Row struct { Name, Node, Status, IP string; VMID int }`
- [x] 5.9 Width rules per design D2 — column maxes: NAME 40, NODE 20
- [x] 5.10 Test `internal/vm/table_test.go`: golden file match for a 3-row fixture
- [x] 5.11 Test `cmd/pmox/list_test.go`: fake PVE server with 3 VMs (2 tagged), assert default output has 2 rows and `--all` has 3
- [x] 5.12 Test `list_test.go` JSON path: `--output json` emits parseable JSON, assert `len(parsed) == 2` for default

## 6. info command

- [x] 6.1 Create `cmd/pmox/info.go` with `newInfoCmd() *cobra.Command`
- [x] 6.2 Register in `main.go`
- [x] 6.3 RunE: `vm.Resolve`, `GetStatus`, `GetConfig`, `AgentNetwork` (ignore error — stale VMs just show blank network)
- [x] 6.4 Create `internal/vm/info.go` with `func RenderInfo(w io.Writer, info Info)` and `type Info struct { Name, Node, Status, Tags, Template string; VMID, CPU int; MemMB int; DiskSize, DiskStorage string; Interfaces []Interface }`
- [x] 6.5 `Template` field: parsed from the VM config's `template` key if present, else blank
- [x] 6.6 Disk parsing: `scsi0` config value is `local-lvm:vm-104-disk-0,size=20G` — split on comma, extract the storage before `:` and the size after `size=`
- [x] 6.7 JSON output: `json.MarshalIndent(info, "", "  ")` — keys are lowercased field names via struct tags
- [x] 6.8 Test `info_test.go`: fake PVE with canned GetConfig and AgentNetwork responses; golden file for text mode; struct-level assertion for JSON

## 7. start command

- [x] 7.1 Create `cmd/pmox/start.go` with `newStartCmd()`
- [x] 7.2 Flags: `--no-wait` (bool), `--wait <duration>` (default 3m)
- [x] 7.3 RunE: `vm.Resolve`, `Start`, `WaitTask`, conditional `launch.WaitForIP`
- [x] 7.4 Output on success: `started <name> (vmid=<id>, ip=<ip>)` or without IP when `--no-wait`
- [x] 7.5 Test `start_test.go`: fake PVE, assert Start+WaitTask+AgentNetwork sequence; `--no-wait` assertion skips AgentNetwork

## 8. stop command

- [x] 8.1 Create `cmd/pmox/stop.go` with `newStopCmd()`
- [x] 8.2 Flags: `--force` (bool, default false), `--no-wait` (bool)
- [x] 8.3 RunE: `vm.Resolve`; if `--force` call `Stop`, else call `Shutdown`; then conditional `WaitTask`
- [x] 8.4 Test `stop_test.go`: fake PVE; assert default routes to `/status/shutdown`, `--force` routes to `/status/stop`

## 9. delete command

- [x] 9.1 Create `cmd/pmox/delete.go` with `newDeleteCmd()`
- [x] 9.2 Flags: `--force` (bool) — skips the pmox-tag check AND uses `Stop` instead of `Shutdown`
- [x] 9.3 RunE sequence from design D5: `Resolve` → `GetStatus` → tag check (unless `--force`) → conditional `Shutdown`/`Stop` + `WaitTask` → `Delete` + `WaitTask`
- [x] 9.4 Tag check: if `!HasPMOXTag(status.Tags)` and no `--force`, return `fmt.Errorf("refusing to delete VM %d (%s): not tagged %q. pass --force to delete anyway.", vmid, name, "pmox")`
- [x] 9.5 Already-gone handling: if `GetStatus` returns `ErrNotFound`, print `VM already deleted; nothing to do` to stderr and return nil
- [x] 9.6 Test `delete_test.go`: fake PVE; scenarios for tagged running VM (full flow), tagged stopped VM (skip shutdown), untagged VM without force (refusal), untagged VM with force (proceeds), `ErrNotFound` (prints already-deleted and exits 0)
- [x] 9.7 Assert zero `DELETE` calls recorded in the untagged-no-force test case

## 10. clone command

- [x] 10.1 Create `cmd/pmox/clone.go` with `newCloneCmd()`
- [x] 10.2 Args: exactly two positional — `<source>` and `<new-name>`
- [x] 10.3 Flags: `--cpu`, `--mem`, `--disk`, `--ssh-key`, `--user`, `--wait`, `--no-wait-ssh` (same semantics as launch)
- [x] 10.4 RunE: `vm.Resolve(source)` → build `launch.Options` with `TemplateID=ref.VMID`, `Node=ref.Node`, `Name=newName` → `launch.Run`
- [x] 10.5 Defaults layered same as launch: CLI flag > configured default > built-in literal
- [x] 10.6 Test `clone_test.go`: fake PVE with a running source VM; assert full launch state machine runs against the source VMID as the template

## 11. pvetest harness refactor

- [x] 11.1 Extract the fake PVE server helper from `internal/launch/launch_test.go` into `internal/pvetest/fake.go`
- [x] 11.2 Make `fakePVE` reusable across packages: methods to register canned responses per path+method, stateful counters for poll loops, captured-request list
- [x] 11.3 Update `internal/launch/launch_test.go` to use the extracted helper — existing tests still pass
- [x] 11.4 Use the helper in every new test file under `cmd/pmox/*_test.go` and `internal/vm/*_test.go`

## 12. Integration

- [x] 12.1 Register all six new commands in `cmd/pmox/main.go`
- [x] 12.2 `pmox --help` shows: `configure, launch, list, info, start, stop, delete, clone`
- [x] 12.3 Every new command's `--help` output includes one-line descriptions for every flag

## 13. Verification

- [x] 13.1 `go build ./...` passes
- [x] 13.2 `go test ./... -race` passes
- [x] 13.3 `make lint` passes
- [ ] 13.4 Manual smoke against a real cluster: `pmox launch x`, `pmox list`, `pmox info x`, `pmox stop x`, `pmox start x`, `pmox clone x y`, `pmox delete x`, `pmox delete y`
