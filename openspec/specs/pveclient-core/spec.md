## Purpose

The `internal/pveclient` package provides the typed Go client for the
Proxmox VE REST API used by every `pmox` subcommand. It owns HTTP
transport, authentication, error-to-sentinel mapping, form-body
encoding, task polling, and the minimal set of VM-lifecycle endpoints
(`Clone`, `Resize`, `SetConfig`, `Start`, `Stop`, `Shutdown`, `Delete`,
`GetStatus`, `AgentNetwork`, `CreateVM`, `ConvertToTemplate`,
`ClusterResources`, `NextID`, `WaitTask`) needed to create, configure,
launch, query, and destroy VMs on a Proxmox host.
## Requirements
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

The client MUST always request a full clone by sending `full=1` in the
form body. The `full` parameter MUST NOT be exposed to callers, and the
client MUST NOT provide an alternative code path that issues a linked
clone. This guarantees the new VM is independent of its source template
so that template upgrades, disk resize, cloud-init rewrites, and
`pmox delete` cannot affect the template.

#### Scenario: Clone issues a POST with the expected form
- **WHEN** `Clone` is called with `node="pve1"`, `sourceID=9000`, `newID=100`, `name="test"`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu/9000/clone`
- **AND** the form body SHALL contain `newid=100`, `name=test`, `full=1`

#### Scenario: Full clone flag is unconditional
- **WHEN** `Clone` is called with any valid arguments
- **THEN** the form body SHALL always contain `full=1`
- **AND** the `Clone` function signature SHALL NOT accept any parameter that would suppress or override the full-clone flag

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

### Requirement: CreateVM endpoint

The client SHALL expose `CreateVM(ctx, node, vmid, kv)` which
issues `POST /nodes/{node}/qemu` to create a new VM from scratch
with the supplied key/value configuration. Returns the PVE task
UPID of the asynchronous create operation.

#### Scenario: CreateVM issues the expected POST
- **WHEN** `CreateVM` is called with `node="pve1"`, `vmid=9000`, and a kv map containing `name=ubuntu-2404-template`, `memory=2048`, `cores=2`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu`
- **AND** the form body SHALL contain `vmid=9000`, `name=ubuntu-2404-template`, `memory=2048`, `cores=2`
- **AND** SHALL return the UPID string parsed from the `{"data": "UPID:..."}` envelope

#### Scenario: import-from value is passed through unchanged
- **WHEN** `CreateVM` is called with a kv entry `scsi0=local-lvm:0,import-from=local:import/noble.qcow2`
- **THEN** the form body SHALL contain `scsi0` with that exact value
- **AND** the client SHALL NOT split or reinterpret the comma-separated disk spec

#### Scenario: Server error propagates
- **WHEN** the PVE API responds with HTTP 500
- **THEN** `CreateVM` SHALL return an error wrapping `ErrAPIError`

#### Scenario: VMID conflict surfaces clearly
- **WHEN** the PVE API responds with HTTP 400 because the VMID already exists
- **THEN** `CreateVM` SHALL return an error whose message includes the upstream PVE error text

### Requirement: ConvertToTemplate endpoint

The client SHALL expose `ConvertToTemplate(ctx, node, vmid)` which
issues `POST /nodes/{node}/qemu/{vmid}/template` to flip an
existing stopped VM into a template.

#### Scenario: ConvertToTemplate issues the expected POST
- **WHEN** `ConvertToTemplate` is called with `node="pve1"`, `vmid=9000`
- **THEN** the client SHALL issue `POST /nodes/pve1/qemu/9000/template` with an empty form body
- **AND** SHALL return nil on HTTP 200

#### Scenario: Running VM rejection propagates
- **WHEN** the PVE API responds with HTTP 400 because the VM is still running
- **THEN** `ConvertToTemplate` SHALL return an error wrapping `ErrAPIError`
- **AND** the error message SHALL include the upstream PVE error text

### Requirement: DownloadURL endpoint

The client SHALL expose `DownloadURL(ctx, node, storage, params)`
which issues `POST /nodes/{node}/storage/{storage}/download-url`
to make the PVE host fetch a remote file into the named storage.
Returns the PVE task UPID of the asynchronous download.

#### Scenario: DownloadURL issues the expected POST
- **WHEN** `DownloadURL` is called with `node="pve1"`, `storage="local"`, and params `url=https://example/img`, `content=import`, `filename=noble.qcow2`, `checksum=abc`, `checksum-algorithm=sha256`
- **THEN** the client SHALL issue `POST /nodes/pve1/storage/local/download-url`
- **AND** the form body SHALL contain each param verbatim
- **AND** SHALL return the UPID string from the `{"data": "UPID:..."}` envelope

#### Scenario: Download task failure is surfaced via WaitTask
- **WHEN** the initial POST returns a UPID and the task subsequently fails (bad checksum, 404 at source URL)
- **THEN** `DownloadURL` SHALL return the UPID successfully
- **AND** the caller's `WaitTask` SHALL later return the task's exit status error wrapped in `ErrAPIError`

#### Scenario: 403 on download-url surfaces permission error
- **WHEN** the PVE API responds with HTTP 403 because the token lacks `Sys.Modify`
- **THEN** `DownloadURL` SHALL return an error wrapping `ErrUnauthorized`

### Requirement: UploadSnippet endpoint

The client SHALL expose `UploadSnippet(ctx, node, storage,
filename, content)` which issues a multipart `POST /nodes/{node}/
storage/{storage}/upload` with `content=snippets` to place a
cloud-init user-data file on the named storage.

#### Scenario: UploadSnippet issues multipart POST
- **WHEN** `UploadSnippet` is called with `node="pve1"`, `storage="local"`, `filename="pmox-qga-bake.yaml"`, and YAML bytes
- **THEN** the client SHALL issue `POST /nodes/pve1/storage/local/upload`
- **AND** the request body SHALL be multipart/form-data with fields `content=snippets`, `filename=pmox-qga-bake.yaml`, and a file part whose bytes match the supplied content
- **AND** SHALL return nil on HTTP 200

#### Scenario: Content-Type includes the multipart boundary
- **WHEN** the upload request is issued
- **THEN** the `Content-Type` header SHALL begin with `multipart/form-data; boundary=`

#### Scenario: Missing snippets content type surfaces a clear error
- **WHEN** the PVE API responds with HTTP 400 because the storage does not allow `snippets` content
- **THEN** `UploadSnippet` SHALL return an error wrapping `ErrAPIError`
- **AND** the error message SHALL include the upstream PVE error text

### Requirement: UpdateStorageContent endpoint

The client SHALL expose `UpdateStorageContent(ctx, storage,
content)` which issues `PUT /storage/{storage}` with the supplied
comma-separated content list to reconfigure a cluster-wide storage
entry. The method is cluster-scoped (no node in the path).

#### Scenario: UpdateStorageContent issues a cluster-wide PUT
- **WHEN** `UpdateStorageContent` is called with `storage="local"` and `content="iso,vztmpl,backup,snippets"`
- **THEN** the client SHALL issue `PUT /storage/local`
- **AND** the form body SHALL contain `content=iso%2Cvztmpl%2Cbackup%2Csnippets`
- **AND** SHALL return nil on HTTP 200

#### Scenario: 403 on storage update surfaces permission error
- **WHEN** the PVE API responds with HTTP 403 because the token lacks `Datastore.Allocate` on `/storage/local`
- **THEN** `UpdateStorageContent` SHALL return an error wrapping `ErrUnauthorized`

### Requirement: No retries on new endpoints

The four new write-path endpoints (`CreateVM`, `ConvertToTemplate`, `DownloadURL`, `UploadSnippet`, `UpdateStorageContent`) SHALL continue the existing pveclient policy of issuing exactly one HTTP call per invocation with no built-in retry, matching every other method in the package.

#### Scenario: Single HTTP call per invocation
- **WHEN** any of the new endpoints is invoked
- **THEN** exactly one HTTP request SHALL reach the test server
- **AND** retry logic SHALL remain the caller's responsibility

