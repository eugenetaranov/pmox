## 1. pveclient extensions

- [x] 1.1 Add `internal/pveclient/cluster.go` declaring `type Resource struct { VMID int "json:\"vmid\""; Name string "json:\"name\""; Node string "json:\"node\""; Status string "json:\"status\""; Tags string "json:\"tags\"" }`.
- [x] 1.2 In the same file, add `func (c *Client) ClusterResources(ctx context.Context, typeFilter string) ([]Resource, error)` that builds path `/cluster/resources` with `?type=<typeFilter>` when `typeFilter != ""`, calls `c.request(ctx, "GET", path, nil)`, and unmarshals `{"data": []Resource}`.
- [x] 1.3 Wrap any parse error with `fmt.Errorf("parse cluster resources: %w", err)`.
- [x] 1.4 Add `internal/pveclient/cluster_test.go` with a fixture in `testdata/cluster_resources.json` containing three entries: two VMs (one tagged `pmox`, one untagged) and one non-VM resource. Assert the `type=vm` filter is passed, three entries parse, and the untagged VM's `Tags` is `""`.
- [x] 1.5 In `internal/pveclient/vm.go`, add `func (c *Client) Shutdown(ctx context.Context, node string, vmid int) (string, error)` — `POST /nodes/{node}/qemu/{vmid}/status/shutdown` via `requestForm(ctx, "POST", path, nil)`, return `parseDataString(body)`.
- [x] 1.6 In `internal/pveclient/vm.go`, add `func (c *Client) Stop(ctx context.Context, node string, vmid int) (string, error)` — `POST /nodes/{node}/qemu/{vmid}/status/stop` via the same pattern.
- [x] 1.7 Add `TestShutdown_HappyPath` and `TestStop_HappyPath` in `vm_test.go` following the existing `TestStart_HappyPath` template: test server asserts method + path, returns `{"data":"UPID:pve1:..."}`, client returns the UPID string.
- [x] 1.8 Add `TestShutdown_ServerError` and `TestStop_ServerError` asserting an error wrapping `ErrAPIError` is returned on HTTP 500.

## 2. internal/vm — shared resolver

- [x] 2.1 Create `internal/vm/resolve.go` with `type Ref struct { VMID int; Node string; Name string; Tags string }`.
- [x] 2.2 Add `func Resolve(ctx context.Context, c *pveclient.Client, arg string) (*Ref, error)`. Implementation: call `c.ClusterResources(ctx, "vm")` once, then branch on `strconv.Atoi(arg)`:
  - On success (numeric): filter by `VMID == n`. Zero matches → `fmt.Errorf("VM %d not found", n)`. One match → return `&Ref{...}`.
  - On failure (name): filter by `r.Name == arg`. Zero → `fmt.Errorf("VM %q not found", arg)`. One → return. Two+ → error `multiple VMs named %q: vmids %v — pass the VMID instead` with VMIDs `sort.Ints`ed for stable output.
- [x] 2.3 Add `func HasPMOXTag(tagsRaw string) bool` that `strings.FieldsFunc`s `tagsRaw` splitting on both `;` and `,`, lowercases each field, and returns true iff any field equals `"pmox"`. Add a short comment naming the PVE version split as the reason both separators are handled.
- [x] 2.4 Create `internal/vm/resolve_test.go`:
  - `TestHasPMOXTag`: table with `""`, `"pmox"`, `"foo;pmox;bar"`, `"foo,pmox"`, `"PMOX"`, `"pmoxish"`, `"notpmox"` → expect `false, true, true, true, true, false, false`.
  - `TestResolve_NumericSingleMatch`, `TestResolve_NameSingleMatch`, `TestResolve_NameAmbiguous`, `TestResolve_NameNotFound`, `TestResolve_VMIDNotFound` — stand up a test server that mirrors `cluster_test.go`'s fixture and exercise each scenario from the spec.

## 3. delete command

- [x] 3.1 Create `cmd/pmox/delete.go` with `func newDeleteCmd() *cobra.Command`. `Use: "delete <name|vmid>"`, `Short: "Stop and destroy a pmox-launched VM"`, `Args: cobra.ExactArgs(1)`.
- [x] 3.2 Add `--force` bool flag on the command. Document in `Long` that `--force` both (a) bypasses the `pmox` tag check and (b) uses hard `stop` instead of graceful `shutdown`.
- [x] 3.3 In `RunE`: build the `pveclient.Client` the same way `launch.go` does (reuse whichever helper `cmd/pmox/launch.go` uses for server resolution + auth). Use `cmd.Context()` so Ctrl-C propagates.
- [x] 3.4 Call `vm.Resolve(ctx, client, args[0])`. On error, return it unchanged.
- [x] 3.5 Tag check: if `!force && !vm.HasPMOXTag(ref.Tags)`, return `fmt.Errorf("refusing to delete VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)`.
- [x] 3.6 Call `status, err := client.GetStatus(ctx, ref.Node, ref.VMID)`. If `errors.Is(err, pveclient.ErrNotFound)`, print `fmt.Fprintf(os.Stderr, "VM %q (vmid %d) is already gone\n", ref.Name, ref.VMID)` and `return nil`. Any other error → return wrapped.
- [x] 3.7 If `status.Status == "running"`: pick `client.Stop` when `--force`, otherwise `client.Shutdown`. Call it, then `client.WaitTask(ctx, ref.Node, upid, 120*time.Second)`. Print progress via the spinner helper the same way `launch.go` does.
- [x] 3.8 Call `client.Delete(ctx, ref.Node, ref.VMID)`, then `client.WaitTask(ctx, ref.Node, upid, 120*time.Second)`. Print progress.
- [x] 3.9 On success print `fmt.Printf("Deleted VM %q (vmid %d)\n", ref.Name, ref.VMID)` to stdout.
- [x] 3.10 In `cmd/pmox/main.go` `init()`, add `rootCmd.AddCommand(newDeleteCmd())` next to the existing `newLaunchCmd()` registration.

## 4. Tests for delete command

- [x] 4.1 Create `cmd/pmox/delete_test.go` mirroring the style of `launch_test.go`. Reuse its fake PVE server pattern where possible.
- [x] 4.2 Scenario: untagged VM + no `--force` → tag-check error, no destructive calls hit the test server (assert via atomic counters).
- [x] 4.3 Scenario: untagged VM + `--force` → proceeds through stop + destroy.
- [x] 4.4 Scenario: running + no `--force` → server sees `status/shutdown`, then `WaitTask`, then `DELETE /qemu/<id>`, then `WaitTask`.
- [x] 4.5 Scenario: running + `--force` → server sees `status/stop` instead of `status/shutdown`.
- [x] 4.6 Scenario: stopped VM → server sees `DELETE` + `WaitTask` only; no `shutdown`/`stop` request recorded.
- [x] 4.7 Scenario: `GetStatus` returns 404 → command exits 0, stderr contains `already gone`, server records no `shutdown`/`stop`/`DELETE` requests.
- [x] 4.8 Scenario: ambiguous name → command exits non-zero before any `GetStatus` call, error lists both VMIDs.

## 5. Validation

- [x] 5.1 `go build ./...` and `go vet ./...` clean.
- [x] 5.2 `go test ./...` clean (including new tests in `internal/pveclient`, `internal/vm`, `cmd/pmox`).
- [x] 5.3 `openspec validate add-delete-command --strict` passes.
- [ ] 5.4 Manual smoke: against a real cluster, `pmox launch` a VM, then `pmox delete <name>` succeeds. Retry `pmox delete <same-name>` — exits 0 with `already gone` note. Create an untagged VM in the web UI, `pmox delete <that-name>` refuses with the tag error, `pmox delete --force <that-name>` destroys it.
