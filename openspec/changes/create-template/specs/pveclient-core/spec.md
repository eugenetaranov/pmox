## ADDED Requirements

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

#### Scenario: importfrom value is passed through unchanged
- **WHEN** `CreateVM` is called with a kv entry `scsi0=local-lvm:0,importfrom=local:iso/noble.img`
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
- **WHEN** `DownloadURL` is called with `node="pve1"`, `storage="local"`, and params `url=https://example/img`, `content=iso`, `filename=noble.img`, `checksum=abc`, `checksum-algorithm=sha256`
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

The four new write-path endpoints (`CreateVM`, `ConvertToTemplate`,
`DownloadURL`, `UploadSnippet`, `UpdateStorageContent`) SHALL
continue the existing pveclient policy of issuing exactly one HTTP
call per invocation with no built-in retry, matching every other
method in the package.

#### Scenario: Single HTTP call per invocation
- **WHEN** any of the new endpoints is invoked
- **THEN** exactly one HTTP request SHALL reach the test server
- **AND** retry logic SHALL remain the caller's responsibility
