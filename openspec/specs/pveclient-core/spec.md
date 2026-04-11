## ADDED Requirements

### Requirement: Form-body HTTP requests

The `internal/pveclient` package SHALL provide an internal helper that
issues authenticated HTTP requests with `application/x-www-form-urlencoded`
bodies, so write-path endpoints (POST, PUT) can pass parameters in the
form required by the Proxmox VE API.

#### Scenario: POST with form body sets Content-Type
- **WHEN** a client method issues a POST with a non-empty form
- **THEN** the request SHALL include header `Content-Type: application/x-www-form-urlencoded`
- **AND** the request body SHALL be the URL-encoded form

#### Scenario: Empty form omits Content-Type
- **WHEN** a client method issues a POST with an empty form
- **THEN** the request SHALL NOT set `Content-Type`
- **AND** the request body SHALL be empty

#### Scenario: Authentication header is always set
- **WHEN** any form-body request is issued
- **THEN** the `Authorization: PVEAPIToken=<id>=<secret>` header SHALL be present

#### Scenario: Status codes map to typed errors
- **WHEN** a form-body request receives HTTP 401
- **THEN** the call SHALL return an error wrapping `ErrUnauthorized`
- **AND** HTTP 5xx SHALL wrap `ErrAPIError`
- **AND** TLS verification failures SHALL wrap `ErrTLSVerificationFailed`

### Requirement: NextID endpoint

The client SHALL expose `NextID(ctx)` which returns the next available
VMID reported by `GET /cluster/nextid`.

#### Scenario: Happy path returns integer
- **WHEN** `NextID` is called and the PVE API responds with `{"data": "100"}`
- **THEN** the method SHALL return the integer `100` and a nil error

#### Scenario: Non-numeric payload is a parse error
- **WHEN** the PVE API responds with `{"data": "not-a-number"}`
- **THEN** `NextID` SHALL return an error wrapping the parse failure with context identifying the response

### Requirement: Clone endpoint

The client SHALL expose `Clone(ctx, node, sourceID, newID, name)` which
issues `POST /nodes/{node}/qemu/{sourceID}/clone` and returns the PVE
task UPID of the asynchronous clone operation.

#### Scenario: Clone issues a POST with the expected form
- **WHEN** `Clone` is called with `node="pve1"`, `sourceID=9000`, `newID=100`, `name="test"`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu/9000/clone`
- **AND** the form body SHALL contain `newid=100`, `name=test`, `full=1`

#### Scenario: UPID is returned on success
- **WHEN** the PVE API responds with `{"data": "UPID:pve1:..."}`
- **THEN** `Clone` SHALL return the full UPID string and a nil error

#### Scenario: Server error propagates
- **WHEN** the PVE API responds with HTTP 500
- **THEN** `Clone` SHALL return an error wrapping `ErrAPIError`

### Requirement: Resize endpoint

The client SHALL expose `Resize(ctx, node, vmid, disk, size)` which
issues `PUT /nodes/{node}/qemu/{vmid}/resize` to grow a VM disk.

#### Scenario: Resize issues a PUT with disk and size
- **WHEN** `Resize` is called with `disk="scsi0"`, `size="+10G"`
- **THEN** the client SHALL issue `PUT /nodes/{node}/qemu/{vmid}/resize`
- **AND** the form body SHALL contain `disk=scsi0` and `size=%2B10G`

#### Scenario: Success returns nil
- **WHEN** the PVE API responds with 200 and an empty data field
- **THEN** `Resize` SHALL return nil

### Requirement: SetConfig endpoint

The client SHALL expose `SetConfig(ctx, node, vmid, kv)` which issues
`POST /nodes/{node}/qemu/{vmid}/config` to push cloud-init and resource
settings as a free-form key/value map.

#### Scenario: Keys are encoded into the form body
- **WHEN** `SetConfig` is called with `kv = {"memory": "2048", "cores": "2"}`
- **THEN** the POST body SHALL contain `memory=2048` and `cores=2`

#### Scenario: sshkeys value is double-encoded
- **WHEN** `SetConfig` is called with `kv = {"sshkeys": "ssh-ed25519 AAAA... user@host"}`
- **THEN** the POST body SHALL contain `sshkeys=<once-url-encoded-value>`
- **AND** the server SHALL receive the sshkeys value as a string that is itself URL-encoded
- **AND** this double encoding SHALL match PVE's documented API contract

### Requirement: Start endpoint

The client SHALL expose `Start(ctx, node, vmid)` which issues
`POST /nodes/{node}/qemu/{vmid}/status/start` and returns the PVE task
UPID of the asynchronous start operation.

#### Scenario: Start issues a POST and returns UPID
- **WHEN** `Start` is called
- **THEN** the client SHALL issue `POST /nodes/{node}/qemu/{vmid}/status/start`
- **AND** SHALL return the UPID from `{"data": "UPID:..."}`

### Requirement: GetStatus endpoint

The client SHALL expose `GetStatus(ctx, node, vmid)` which issues
`GET /nodes/{node}/qemu/{vmid}/status/current` and returns a parsed
`VMStatus` struct describing the VM's current state.

#### Scenario: Running VM status is parsed
- **WHEN** the PVE API responds with a VM in the `running` state
- **THEN** `GetStatus` SHALL return a non-nil `*VMStatus`
- **AND** `Status` SHALL equal `"running"`
- **AND** `VMID`, `Name`, `Uptime`, `Mem`, and `MaxMem` SHALL be populated from the response

### Requirement: Delete endpoint

The client SHALL expose `Delete(ctx, node, vmid)` which issues
`DELETE /nodes/{node}/qemu/{vmid}` and returns the PVE task UPID of
the asynchronous destroy operation.

#### Scenario: Delete issues a DELETE and returns UPID
- **WHEN** `Delete` is called
- **THEN** the client SHALL issue `DELETE /nodes/{node}/qemu/{vmid}` with no body
- **AND** SHALL return the UPID from `{"data": "UPID:..."}`

### Requirement: AgentNetwork endpoint

The client SHALL expose `AgentNetwork(ctx, node, vmid)` which issues
`GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces` and
returns the qemu-guest-agent's view of the VM's network interfaces.

#### Scenario: Interface list is parsed from double-wrapped response
- **WHEN** the PVE API responds with `{"data": {"result": [ ... ]}}`
- **THEN** `AgentNetwork` SHALL return the list of `AgentIface` entries from the `result` array
- **AND** each entry SHALL have its name, hardware address, and IP addresses populated

#### Scenario: Agent-not-running error propagates
- **WHEN** the PVE API responds with HTTP 500 because the guest agent is not running
- **THEN** `AgentNetwork` SHALL return an error wrapping `ErrAPIError` without any special-casing

#### Scenario: No built-in retry
- **WHEN** the guest agent is slow to come up
- **THEN** `AgentNetwork` SHALL still return after a single HTTP call
- **AND** retry logic SHALL be the caller's responsibility

### Requirement: WaitTask helper

The client SHALL expose `WaitTask(ctx, node, upid, timeout)` which
polls `GET /nodes/{node}/tasks/{upid}/status` until the referenced
PVE task completes, the context is cancelled, or the timeout elapses.

#### Scenario: Running task resolves to success
- **WHEN** the task transitions from `running` to `stopped` with `exitstatus="OK"`
- **THEN** `WaitTask` SHALL return nil

#### Scenario: Failed task surfaces exit status
- **WHEN** the task transitions to `stopped` with `exitstatus="clone failed: destination VMID 200 already exists"`
- **THEN** `WaitTask` SHALL return an error wrapping `ErrAPIError`
- **AND** the error message SHALL contain the task's exit status text and the UPID

#### Scenario: Context cancellation is honored
- **WHEN** the caller's context is cancelled while `WaitTask` is polling
- **THEN** `WaitTask` SHALL return the context error

#### Scenario: Cancelled context before first poll
- **WHEN** `WaitTask` is called with an already-cancelled context
- **THEN** it SHALL return the context error
- **AND** SHALL NOT issue any HTTP request

#### Scenario: Timeout is honored
- **WHEN** the task remains `running` past the supplied timeout
- **THEN** `WaitTask` SHALL return an error wrapping `ErrTimeout`
- **AND** the error message SHALL identify the UPID

### Requirement: No secrets in logs

No method in `internal/pveclient` SHALL emit the token secret in any
log, error message, or response body written for debugging.

#### Scenario: Transport capture sees no secret
- **WHEN** a test wraps the HTTP transport with a recorder
- **AND** drives the client through any of the new endpoints
- **THEN** the recorded request bodies and headers SHALL never contain the token secret value
