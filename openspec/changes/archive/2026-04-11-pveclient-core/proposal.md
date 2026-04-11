## Why

Slice 2 shipped a deliberately minimal `internal/pveclient` — just
`GetVersion` plus the four list endpoints `configure` needs for auto-
discovery. Slice 5 (`launch-default`) cannot start until the client
knows how to actually *do* things: allocate a VMID, clone a template,
resize its disk, push a cloud-init config, start it, wait for the
guest agent, tear it down again. Dropping all of that into slice 5
would make that slice huge and mix plumbing with policy.

This slice is the "plumbing" half: every HTTP endpoint the launch /
lifecycle commands will call, with no CLI surface of its own. Slice 5
then becomes a thin orchestrator over this API.

## What Changes

- Extend `internal/pveclient/client.go` `request()` helper to support
  POST/PUT with form-encoded bodies. Current `request()` only passes
  method + path + query; PVE's write endpoints expect
  `application/x-www-form-urlencoded` bodies.
- Add `NextID(ctx) (int, error)` — `GET /cluster/nextid`. Returns the
  lowest free VMID.
- Add `Clone(ctx, node string, sourceID, newID int, name string) (string, error)`
  — `POST /nodes/{node}/qemu/{vmid}/clone`. Returns the UPID of the
  resulting PVE task so callers can track completion.
- Add `Resize(ctx, node string, vmid int, disk, size string) error`
  — `PUT /nodes/{node}/qemu/{vmid}/resize`. `disk` is typically
  `scsi0`, `size` is in PVE's `+NG` / absolute `NG` format.
- Add `SetConfig(ctx, node string, vmid int, kv map[string]string) error`
  — `POST /nodes/{node}/qemu/{vmid}/config`. Used to push cloud-init
  keys (`ciuser`, `sshkeys`, `ipconfig0`, `cicustom`) and resource
  settings (`memory`, `cores`, `agent`, `name`).
- Add `Start(ctx, node string, vmid int) (string, error)` —
  `POST /nodes/{node}/qemu/{vmid}/status/start`. Returns UPID.
- Add `GetStatus(ctx, node string, vmid int) (VMStatus, error)` —
  `GET /nodes/{node}/qemu/{vmid}/status/current`. Returns the parsed
  status block (status string, qmpstatus, uptime, etc.) so launch can
  poll for `running`.
- Add `AgentNetwork(ctx, node string, vmid int) ([]AgentIface, error)`
  — `GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`.
  Returns the qemu-guest-agent's view of the VM's NICs. Used by
  launch to discover the VM's DHCP-assigned IP.
- Add `Delete(ctx, node string, vmid int) (string, error)` —
  `DELETE /nodes/{node}/qemu/{vmid}`. Returns UPID. Caller is
  responsible for stopping the VM first.
- Add `WaitTask(ctx, node, upid string, timeout time.Duration) error`
  — polls `GET /nodes/{node}/tasks/{upid}/status` until the task
  exits. Returns nil on success, wrapped `ErrAPIError` on failure
  with the task's exit status as context. This is the shared
  "wait for PVE task to finish" helper every write-path caller
  needs.
- Add table-driven unit tests for every new endpoint using
  `httptest.Server` — happy path plus at least one error path per
  endpoint. Task-wait gets its own test with a multi-step mock
  server that returns "running" then "stopped".

## Capabilities

### New Capabilities
- `pveclient-core`: a Go API over the Proxmox VE REST endpoints
  needed to launch, inspect, and destroy VMs. No CLI surface — pure
  library code consumed by later slices.

### Modified Capabilities
- `configure-and-credstore`: the existing minimal client (`GetVersion`,
  `ListNodes`, `ListTemplates`, `ListStorage`, `ListBridges`) moves
  into the same package as the new endpoints. `configure` keeps
  working byte-for-byte — the existing calls are untouched; this
  slice only adds new methods and a form-body branch in `request()`.

## Impact

- **New files**: `internal/pveclient/vm.go` (Clone, Start, Delete,
  SetConfig, Resize, GetStatus), `internal/pveclient/nextid.go`
  (trivial one-function file, kept separate so it's obvious),
  `internal/pveclient/agent.go` (AgentNetwork), `internal/pveclient/tasks.go`
  (WaitTask), plus `_test.go` siblings for each.
- **Modified files**: `internal/pveclient/client.go` — `request()`
  gains an optional body parameter and Content-Type handling.
  Existing callers keep working because a new overload
  (`requestForm`) is added alongside, leaving `request()` as it was.
- **No new dependencies**: Go stdlib only. PVE's form encoding is
  just `net/url.Values.Encode`.
- **No user-visible behavior change**: this slice adds no commands.
  `pmox --help` output is unchanged. Slice 5 is the first real
  consumer.
- **Cross-slice contract**: this slice produces the full
  `*pveclient.Client` surface that `launch-default` (slice 5) and
  `list-info-lifecycle` (slice 6) will consume. It reads no config,
  writes no files, exposes no CLI — pure library.
