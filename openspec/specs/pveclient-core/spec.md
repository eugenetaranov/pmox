## ADDED Requirements

### Requirement: ClusterResources endpoint

The client SHALL expose `ClusterResources(ctx, typeFilter string) ([]Resource, error)`
which issues `GET /cluster/resources?type={typeFilter}` and returns
a summary entry per resource.

#### Scenario: VM-type query returns VM summaries
- **WHEN** `ClusterResources(ctx, "vm")` is called
- **THEN** the client SHALL issue `GET /cluster/resources?type=vm`
- **AND** SHALL return one `Resource` per VM on the cluster

#### Scenario: Resource entries include the fields needed by lifecycle commands
- **WHEN** `ClusterResources` parses a response
- **THEN** each `Resource` SHALL have `Name`, `VMID`, `Node`, `Status`, and `Tags` populated from the response fields
- **AND** `Tags` SHALL be the raw comma-separated string as PVE returns it

### Requirement: GetConfig endpoint

The client SHALL expose `GetConfig(ctx, node string, vmid int) (map[string]string, error)`
which issues `GET /nodes/{node}/qemu/{vmid}/config` and returns
every config key-value pair as a string map.

#### Scenario: Config map contains every returned key
- **WHEN** `GetConfig` is called against a VM with `cores=2`, `memory=2048`, `net0=virtio=...`, `scsi0=local-lvm:vm-104-disk-0,size=20G`
- **THEN** the returned map SHALL contain all four keys with their raw string values

### Requirement: Shutdown endpoint

The client SHALL expose `Shutdown(ctx, node string, vmid int) (upid string, err error)`
which issues `POST /nodes/{node}/qemu/{vmid}/status/shutdown` and
returns the PVE task UPID of the asynchronous graceful-shutdown
operation.

#### Scenario: Shutdown issues a POST and returns UPID
- **WHEN** `Shutdown` is called
- **THEN** the client SHALL issue `POST /nodes/{node}/qemu/{vmid}/status/shutdown` with no body
- **AND** SHALL return the UPID from `{"data": "UPID:..."}`

### Requirement: Stop endpoint

The client SHALL expose `Stop(ctx, node string, vmid int) (upid string, err error)`
which issues `POST /nodes/{node}/qemu/{vmid}/status/stop` (hard
power off) and returns the PVE task UPID.

#### Scenario: Stop issues a POST and returns UPID
- **WHEN** `Stop` is called
- **THEN** the client SHALL issue `POST /nodes/{node}/qemu/{vmid}/status/stop` with no body
- **AND** SHALL return the UPID from `{"data": "UPID:..."}`

## REMOVED Requirements

### Requirement: UploadSnippet endpoint
**Reason**: The PVE HTTP upload endpoint rejects `content=snippets`
server-side on stock PVE 8.x (the `content` parameter enum is hardcoded
to `iso, vztmpl, import`), so this method never worked against a real
cluster. Snippet uploads now happen via SSH/SFTP through the new
`internal/pvessh` package.
**Migration**: Callers SHALL open a `pvessh.Client` via `pvessh.Dial`
and invoke `(*Client).UploadSnippet(ctx, storagePath, filename, content)`.
The storage path is obtained by calling `GET /storage/{storage}` and
reading the `path` field.

### Requirement: UpdateStorageContent endpoint
**Reason**: This method existed only to append `snippets` to a storage
pool's cluster-wide `content=` list as a prerequisite for the HTTP
snippet upload. With the upload path replaced by SSH/SFTP, mutating
the PVE storage config is no longer needed and was an unwanted side
effect on user clusters.
**Migration**: None. There is no replacement; pmox SHALL NOT mutate
cluster-wide storage configuration. Users whose storage pools already
had `snippets` content enabled are unaffected; users whose pools did
not will simply never see the mutation happen, and SCP still writes
files successfully because the filesystem write is independent of the
PVE storage content whitelist.
