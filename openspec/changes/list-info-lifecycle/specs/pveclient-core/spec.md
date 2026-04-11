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
