## ADDED Requirements

### Requirement: `pmox list` command

The CLI SHALL expose `pmox list` which enumerates VMs on the
resolved cluster. By default it shows only VMs tagged `pmox`.

#### Scenario: Default list shows only pmox-tagged VMs
- **WHEN** `pmox list` is invoked against a fake cluster with 3 VMs, 2 tagged `pmox` and 1 untagged
- **THEN** the output table SHALL contain 2 rows
- **AND** the untagged VM SHALL NOT appear

#### Scenario: `--all` shows every VM
- **WHEN** `pmox list --all` is invoked against the same cluster
- **THEN** the output table SHALL contain 3 rows

#### Scenario: Table columns are fixed
- **WHEN** `pmox list` prints output in table mode
- **THEN** the header row SHALL be `NAME VMID NODE STATUS IP`
- **AND** each data row SHALL have those five columns in that order

#### Scenario: JSON output
- **WHEN** `pmox list --output json` is invoked
- **THEN** stdout SHALL be a JSON array of objects
- **AND** each object SHALL have keys `name`, `vmid`, `node`, `status`, `ip`

#### Scenario: IP column is blank for stopped VMs
- **WHEN** a VM in the table has status `stopped`
- **THEN** the IP cell SHALL be rendered as `-` in table mode
- **AND** the `ip` field SHALL be an empty string in JSON mode
- **AND** the launcher SHALL NOT call `AgentNetwork` for that VM

### Requirement: `pmox info` command

The CLI SHALL expose `pmox info <name|vmid>` which prints detailed
information about a single VM.

#### Scenario: Info prints configured and runtime fields
- **WHEN** `pmox info web1` is invoked against a running VM
- **THEN** the output SHALL include the name, VMID, node, status, tags, CPU cores, memory, primary disk, and network interfaces
- **AND** the command SHALL exit 0

#### Scenario: Info accepts numeric VMID
- **WHEN** `pmox info 104` is invoked and VM 104 exists
- **THEN** the command SHALL print the same fields as the by-name lookup

#### Scenario: Info on an unknown name surfaces not-found
- **WHEN** `pmox info nonexistent` is invoked
- **THEN** the command SHALL exit `ExitConfig`
- **AND** the error message SHALL contain `not found`

### Requirement: `pmox start` command

The CLI SHALL expose `pmox start <name|vmid>` which starts a
stopped VM and (by default) waits for the guest agent to report
an IP.

#### Scenario: Start waits for IP by default
- **WHEN** `pmox start web1` is invoked against a stopped VM
- **THEN** the command SHALL call `Start`, `WaitTask`, and poll `AgentNetwork` until an IP is available
- **AND** the command SHALL print `started web1 (vmid=<id>, ip=<ip>)` on success

#### Scenario: `--no-wait` returns after the start task completes
- **WHEN** `pmox start --no-wait web1` is invoked
- **THEN** the command SHALL call `Start` and `WaitTask` but SHALL NOT call `AgentNetwork`
- **AND** the success message SHALL omit the IP field

### Requirement: `pmox stop` command

The CLI SHALL expose `pmox stop <name|vmid>` which gracefully
shuts down a running VM. `--force` SHALL issue a hard power-off
instead.

#### Scenario: Default stop is ACPI shutdown
- **WHEN** `pmox stop web1` is invoked
- **THEN** the command SHALL call `Shutdown` (POST `/status/shutdown`)
- **AND** SHALL wait for the resulting task to finish

#### Scenario: `--force` is a hard stop
- **WHEN** `pmox stop --force web1` is invoked
- **THEN** the command SHALL call `Stop` (POST `/status/stop`)

#### Scenario: `--no-wait` short-circuits the task wait
- **WHEN** `pmox stop --no-wait web1` is invoked
- **THEN** the command SHALL return as soon as the Shutdown call returns a UPID

### Requirement: `pmox delete` command

The CLI SHALL expose `pmox delete <name|vmid>` which stops the VM
if running and then destroys it.

#### Scenario: Delete refuses untagged VMs
- **WHEN** `pmox delete web1` is invoked against a VM whose tags do not contain `pmox`
- **THEN** the command SHALL exit with an error containing `not tagged "pmox"`
- **AND** the error SHALL suggest `--force`
- **AND** SHALL NOT issue any destructive API calls

#### Scenario: `--force` bypasses the tag check
- **WHEN** `pmox delete --force web1` is invoked against an untagged VM
- **THEN** the command SHALL proceed with stop + destroy

#### Scenario: Running VM is shut down before destroy
- **WHEN** `pmox delete web1` is invoked against a running VM
- **THEN** the command SHALL issue Shutdown, WaitTask, Delete, WaitTask in that order

#### Scenario: Stopped VM skips shutdown
- **WHEN** `pmox delete web1` is invoked against a stopped VM
- **THEN** the command SHALL issue only Delete + WaitTask

#### Scenario: Already-gone VM is treated as success
- **WHEN** the initial `GetStatus` returns `ErrNotFound`
- **THEN** the command SHALL print `VM already deleted; nothing to do` to stderr
- **AND** SHALL exit 0

#### Scenario: `--force` uses hard stop instead of shutdown
- **WHEN** `pmox delete --force web1` is invoked against a running VM
- **THEN** the stop phase SHALL issue `Stop` (POST `/status/stop`), not `Shutdown`

### Requirement: `pmox clone` command

The CLI SHALL expose `pmox clone <source-name|vmid> <new-name>`
which creates a new VM by cloning an existing VM (template or
regular VM).

#### Scenario: Clone reuses the launch state machine
- **WHEN** `pmox clone web1 web1-copy` is invoked
- **THEN** the command SHALL invoke `launch.Run` with `TemplateID` set to the resolved source VMID
- **AND** the resulting VM SHALL be tagged `pmox`

#### Scenario: Clone accepts size-override flags
- **WHEN** `pmox clone --cpu 4 --mem 8192 web1 web1-copy` is invoked
- **THEN** the cloned VM's config SHALL reflect the override values
- **AND** flags unset on the clone command SHALL inherit from configured defaults

### Requirement: Name↔VMID resolver

The `list-info-lifecycle` package SHALL expose
`vm.Resolve(ctx, client, arg)` returning a `*Ref` containing
`VMID`, `Node`, and `Name`. The resolver SHALL accept either the
VM name or a decimal VMID.

#### Scenario: Numeric argument resolves by VMID
- **WHEN** `Resolve` is called with `arg="104"` and VM 104 exists
- **THEN** the returned `Ref.VMID` SHALL equal 104

#### Scenario: Name argument resolves by exact match
- **WHEN** `Resolve` is called with `arg="web1"` and exactly one VM is named `web1`
- **THEN** the returned `Ref.Name` SHALL equal `web1`
- **AND** the returned `Ref.VMID` SHALL equal that VM's id

#### Scenario: Duplicate names are a hard error
- **WHEN** `Resolve` is called with `arg="web1"` and two VMs share that name
- **THEN** `Resolve` SHALL return an error containing `multiple VMs named "web1"`
- **AND** the error SHALL list each matching VMID
- **AND** the error SHALL instruct the user to pass the VMID instead

#### Scenario: Unknown name is not-found
- **WHEN** `Resolve` is called with `arg="nonexistent"` and no VM matches
- **THEN** `Resolve` SHALL return an error containing `not found`

### Requirement: Parallel IP lookup in `list`

`pmox list` SHALL fetch IPs for running VMs in parallel with a
concurrency cap to avoid blocking the whole listing on one slow
guest agent.

#### Scenario: Slow guest agent does not block other rows
- **WHEN** one VM's `AgentNetwork` call takes 10 seconds to respond and another's returns immediately
- **THEN** the rows for every other VM SHALL populate without waiting for the slow one up to the concurrency limit
- **AND** the concurrency limit SHALL be 8 simultaneous `AgentNetwork` calls

#### Scenario: Agent error leaves the IP column blank
- **WHEN** `AgentNetwork` returns an error for a running VM
- **THEN** that row's IP cell SHALL be blank
- **AND** the command SHALL still exit 0

