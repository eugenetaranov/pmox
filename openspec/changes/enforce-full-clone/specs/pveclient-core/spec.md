## MODIFIED Requirements

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
