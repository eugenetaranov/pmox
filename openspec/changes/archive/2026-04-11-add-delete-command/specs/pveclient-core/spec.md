## ADDED Requirements

### Requirement: Shutdown endpoint

The client SHALL expose `Shutdown(ctx, node, vmid)` which issues `POST /nodes/{node}/qemu/{vmid}/status/shutdown` and returns the PVE task UPID of the asynchronous graceful shutdown (ACPI) operation.

#### Scenario: Shutdown issues the expected POST
- **WHEN** `Shutdown` is called with `node="pve1"`, `vmid=104`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu/104/status/shutdown`
- **AND** SHALL return the UPID string parsed from the `{"data": "UPID:..."}` envelope

#### Scenario: Shutdown propagates API errors
- **WHEN** the PVE API responds with HTTP 500
- **THEN** `Shutdown` SHALL return an error wrapping `ErrAPIError`

### Requirement: Stop endpoint

The client SHALL expose `Stop(ctx, node, vmid)` which issues `POST /nodes/{node}/qemu/{vmid}/status/stop` and returns the PVE task UPID of the asynchronous hard power-off operation.

#### Scenario: Stop issues the expected POST
- **WHEN** `Stop` is called with `node="pve1"`, `vmid=104`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu/104/status/stop`
- **AND** SHALL return the UPID string parsed from the `{"data": "UPID:..."}` envelope

#### Scenario: Stop propagates API errors
- **WHEN** the PVE API responds with HTTP 500
- **THEN** `Stop` SHALL return an error wrapping `ErrAPIError`

### Requirement: ClusterResources endpoint

The client SHALL expose `ClusterResources(ctx, typeFilter)` returning a slice of `Resource{VMID int, Name, Node, Status, Tags string}`. The method SHALL issue `GET /cluster/resources?type=<typeFilter>` when `typeFilter` is non-empty, omitting the query string otherwise. Tag and status fields SHALL be copied verbatim from the PVE response (no normalization at the client layer).

#### Scenario: Fetching VMs returns parsed resources
- **WHEN** `ClusterResources(ctx, "vm")` is called and the API returns two VM entries
- **THEN** the client SHALL issue `GET /cluster/resources?type=vm`
- **AND** SHALL return two `Resource` values with `VMID`, `Name`, `Node`, `Status`, and `Tags` populated from the response

#### Scenario: Missing tags field parses as empty string
- **WHEN** a resource in the response omits the `tags` field
- **THEN** the corresponding `Resource.Tags` SHALL be the empty string
