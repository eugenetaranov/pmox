## 1. pveclient extensions

- [ ] 1.1 Add `internal/pveclient/cluster.go` with `type Resource struct { Name, Node, Status, Tags string; VMID int }` and json tags
- [ ] 1.2 `func (c *Client) ClusterResources(ctx context.Context, typeFilter string) ([]Resource, error)` — GET `/cluster/resources?type=<filter>` via `request()`
- [ ] 1.3 Parse response `{"data": [...]}` into `[]Resource`
- [ ] 1.4 Test `cluster_test.go`: fixture with 3 entries (2 VMs tagged `pmox`, 1 untagged), assert parsing
- [ ] 1.5 Add `func (c *Client) GetConfig(ctx context.Context, node string, vmid int) (map[string]string, error)` in `vm.go`
- [ ] 1.6 GetConfig parsing: response is `{"data": {"cores": 2, "memory": 2048, "net0": "virtio=..."}}` — values may be int or string. Convert everything to string via `fmt.Sprintf("%v", v)`
- [ ] 1.7 Test `TestGetConfig`: fixture with mixed int/string values, assert all keys present and stringified
- [ ] 1.8 Add `func (c *Client) Shutdown(ctx context.Context, node string, vmid int) (string, error)` in `vm.go` — POST `/status/shutdown`, parse UPID
- [ ] 1.9 Add `func (c *Client) Stop(ctx context.Context, node string, vmid int) (string, error)` in `vm.go` — POST `/status/stop`, parse UPID
- [ ] 1.10 Test `TestShutdown`, `TestStop`: mock server asserts method and path, returns UPID

## 2. internal/vm — shared resolver

- [ ] 2.1 Create `internal/vm/resolve.go` with `type Ref struct { VMID int; Node, Name string }`
- [ ] 2.2 `func Resolve(ctx context.Context, c *pveclient.Client, arg string) (*Ref, error)`
- [ ] 2.3 Numeric path: if `strconv.Atoi(arg)` succeeds, call `ClusterResources(ctx, "vm")` and filter by VMID; one match → return, zero → `not found: vmid %d`
- [ ] 2.4 Name path: call `ClusterResources(ctx, "vm")`, filter by `r.Name == arg`
- [ ] 2.5 Zero matches by name: return error `VM %q not found`
- [ ] 2.6 Two+ matches by name: return error `multiple VMs named %q: vmids %v — pass the VMID instead`, with VMIDs sorted ascending for stable output
- [ ] 2.7 Test `resolve_test.go` with table cases per scenarios in spec.md

## 3. internal/vm — tag check

- [ ] 3.1 Add `func HasPMOXTag(tagsRaw string) bool` in `internal/vm/resolve.go` — splits on `;` and `,`, case-insensitive match for `pmox`
- [ ] 3.2 Test table: `""`, `"pmox"`, `"foo;pmox"`, `"pmox;bar"`, `"PMOX"`, `"notpmox"`, `"pmoxish"` — only the first four (and `PMOX`) return true
- [ ] 3.3 Document with comment: PVE's tag format varies between `;` and `,` separated across versions — handle both

## 4. internal/launch — export PickIPv4

- [ ] 4.1 Rename `pickIPv4` → `PickIPv4` in `internal/launch/ip.go`
- [ ] 4.2 Update in-package test calls to use the exported name
- [ ] 4.3 Godoc comment: "PickIPv4 implements the D-T3 heuristic shared between launch and list."

## 5. list command

- [ ] 5.1 Create `cmd/pmox/list.go` with `newListCmd() *cobra.Command`
- [ ] 5.2 Flags: `--all` (bool), output is handled via root persistent `--output`
- [ ] 5.3 Register in `cmd/pmox/main.go` via `rootCmd.AddCommand(newListCmd())`
- [ ] 5.4 RunE: resolve server, call `ClusterResources(ctx, "vm")`, client-side filter by `HasPMOXTag` unless `--all`
- [ ] 5.5 For each running VM, fetch IP via `AgentNetwork` in an `errgroup.WithContext` with `SetLimit(8)`
- [ ] 5.6 Render table or JSON based on `--output`; table path lives in `internal/vm/table.go`
- [ ] 5.7 Create `internal/vm/table.go` with `func RenderTable(w io.Writer, rows []Row)` using `fmt.Fprintf` and width computation
- [ ] 5.8 `type Row struct { Name, Node, Status, IP string; VMID int }`
- [ ] 5.9 Width rules per design D2 — column maxes: NAME 40, NODE 20
- [ ] 5.10 Test `internal/vm/table_test.go`: golden file match for a 3-row fixture
- [ ] 5.11 Test `cmd/pmox/list_test.go`: fake PVE server with 3 VMs (2 tagged), assert default output has 2 rows and `--all` has 3
- [ ] 5.12 Test `list_test.go` JSON path: `--output json` emits parseable JSON, assert `len(parsed) == 2` for default

## 6. info command

- [ ] 6.1 Create `cmd/pmox/info.go` with `newInfoCmd() *cobra.Command`
- [ ] 6.2 Register in `main.go`
- [ ] 6.3 RunE: `vm.Resolve`, `GetStatus`, `GetConfig`, `AgentNetwork` (ignore error — stale VMs just show blank network)
- [ ] 6.4 Create `internal/vm/info.go` with `func RenderInfo(w io.Writer, info Info)` and `type Info struct { Name, Node, Status, Tags, Template string; VMID, CPU int; MemMB int; DiskSize, DiskStorage string; Interfaces []Interface }`
- [ ] 6.5 `Template` field: parsed from the VM config's `template` key if present, else blank
- [ ] 6.6 Disk parsing: `scsi0` config value is `local-lvm:vm-104-disk-0,size=20G` — split on comma, extract the storage before `:` and the size after `size=`
- [ ] 6.7 JSON output: `json.MarshalIndent(info, "", "  ")` — keys are lowercased field names via struct tags
- [ ] 6.8 Test `info_test.go`: fake PVE with canned GetConfig and AgentNetwork responses; golden file for text mode; struct-level assertion for JSON

## 7. start command

- [ ] 7.1 Create `cmd/pmox/start.go` with `newStartCmd()`
- [ ] 7.2 Flags: `--no-wait` (bool), `--wait <duration>` (default 3m)
- [ ] 7.3 RunE: `vm.Resolve`, `Start`, `WaitTask`, conditional `launch.WaitForIP`
- [ ] 7.4 Output on success: `started <name> (vmid=<id>, ip=<ip>)` or without IP when `--no-wait`
- [ ] 7.5 Test `start_test.go`: fake PVE, assert Start+WaitTask+AgentNetwork sequence; `--no-wait` assertion skips AgentNetwork

## 8. stop command

- [ ] 8.1 Create `cmd/pmox/stop.go` with `newStopCmd()`
- [ ] 8.2 Flags: `--force` (bool, default false), `--no-wait` (bool)
- [ ] 8.3 RunE: `vm.Resolve`; if `--force` call `Stop`, else call `Shutdown`; then conditional `WaitTask`
- [ ] 8.4 Test `stop_test.go`: fake PVE; assert default routes to `/status/shutdown`, `--force` routes to `/status/stop`

## 9. delete command

- [ ] 9.1 Create `cmd/pmox/delete.go` with `newDeleteCmd()`
- [ ] 9.2 Flags: `--force` (bool) — skips the pmox-tag check AND uses `Stop` instead of `Shutdown`
- [ ] 9.3 RunE sequence from design D5: `Resolve` → `GetStatus` → tag check (unless `--force`) → conditional `Shutdown`/`Stop` + `WaitTask` → `Delete` + `WaitTask`
- [ ] 9.4 Tag check: if `!HasPMOXTag(status.Tags)` and no `--force`, return `fmt.Errorf("refusing to delete VM %d (%s): not tagged %q. pass --force to delete anyway.", vmid, name, "pmox")`
- [ ] 9.5 Already-gone handling: if `GetStatus` returns `ErrNotFound`, print `VM already deleted; nothing to do` to stderr and return nil
- [ ] 9.6 Test `delete_test.go`: fake PVE; scenarios for tagged running VM (full flow), tagged stopped VM (skip shutdown), untagged VM without force (refusal), untagged VM with force (proceeds), `ErrNotFound` (prints already-deleted and exits 0)
- [ ] 9.7 Assert zero `DELETE` calls recorded in the untagged-no-force test case

## 10. clone command

- [ ] 10.1 Create `cmd/pmox/clone.go` with `newCloneCmd()`
- [ ] 10.2 Args: exactly two positional — `<source>` and `<new-name>`
- [ ] 10.3 Flags: `--cpu`, `--mem`, `--disk`, `--ssh-key`, `--user`, `--wait`, `--no-wait-ssh` (same semantics as launch)
- [ ] 10.4 RunE: `vm.Resolve(source)` → build `launch.Options` with `TemplateID=ref.VMID`, `Node=ref.Node`, `Name=newName` → `launch.Run`
- [ ] 10.5 Defaults layered same as launch: CLI flag > configured default > built-in literal
- [ ] 10.6 Test `clone_test.go`: fake PVE with a running source VM; assert full launch state machine runs against the source VMID as the template

## 11. pvetest harness refactor

- [ ] 11.1 Extract the fake PVE server helper from `internal/launch/launch_test.go` into `internal/pvetest/fake.go`
- [ ] 11.2 Make `fakePVE` reusable across packages: methods to register canned responses per path+method, stateful counters for poll loops, captured-request list
- [ ] 11.3 Update `internal/launch/launch_test.go` to use the extracted helper — existing tests still pass
- [ ] 11.4 Use the helper in every new test file under `cmd/pmox/*_test.go` and `internal/vm/*_test.go`

## 12. Integration

- [ ] 12.1 Register all six new commands in `cmd/pmox/main.go`
- [ ] 12.2 `pmox --help` shows: `configure, launch, list, info, start, stop, delete, clone`
- [ ] 12.3 Every new command's `--help` output includes one-line descriptions for every flag

## 13. Verification

- [ ] 13.1 `go build ./...` passes
- [ ] 13.2 `go test ./... -race` passes
- [ ] 13.3 `make lint` passes
- [ ] 13.4 Manual smoke against a real cluster: `pmox launch x`, `pmox list`, `pmox info x`, `pmox stop x`, `pmox start x`, `pmox clone x y`, `pmox delete x`, `pmox delete y`
