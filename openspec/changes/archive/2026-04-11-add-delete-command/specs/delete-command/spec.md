## ADDED Requirements

### Requirement: `pmox delete` command

The CLI SHALL expose `pmox delete <name|vmid>` which resolves the argument to a single VM on the configured cluster, stops it if running, and then destroys it via `DELETE /nodes/{node}/qemu/{vmid}`. The command SHALL wait for every underlying PVE task to reach a terminal state before returning.

The command SHALL refuse to destroy a VM whose tags do not contain `pmox` unless the user passes `--force`. The error message SHALL name the VM, state that it is not tagged `pmox`, and suggest `--force` as the bypass.

#### Scenario: Tag check blocks untagged VMs
- **WHEN** `pmox delete web1` is invoked against a VM whose tags do not contain `pmox`
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL contain `not tagged "pmox"` and the VM's name or VMID
- **AND** the error SHALL mention `--force`
- **AND** the command SHALL NOT issue any `Shutdown`, `Stop`, or `Delete` API call

#### Scenario: `--force` bypasses the tag check
- **WHEN** `pmox delete --force web1` is invoked against an untagged VM
- **THEN** the command SHALL proceed with the stop + destroy sequence

#### Scenario: Running VM is shut down before destroy
- **WHEN** `pmox delete web1` is invoked against a VM whose status is `running`
- **THEN** the command SHALL issue `Shutdown`, `WaitTask`, `Delete`, `WaitTask` in that order against the resolved node and VMID

#### Scenario: Stopped VM skips shutdown
- **WHEN** `pmox delete web1` is invoked against a VM whose status is `stopped`
- **THEN** the command SHALL issue `Delete` and `WaitTask` only
- **AND** the command SHALL NOT issue `Shutdown` or `Stop`

#### Scenario: `--force` uses hard stop instead of shutdown
- **WHEN** `pmox delete --force web1` is invoked against a running VM
- **THEN** the stop phase SHALL issue `Stop` (POST `/status/stop`), not `Shutdown`

#### Scenario: Already-gone VM is treated as success
- **WHEN** the initial `GetStatus` call returns an error wrapping `ErrNotFound`
- **THEN** the command SHALL print a note to stderr stating the VM is already gone
- **AND** the command SHALL exit 0
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Ambiguous name fails before any destructive call
- **WHEN** two or more VMs on the cluster share the name passed to `pmox delete`
- **THEN** the command SHALL exit non-zero with an error listing the matching VMIDs
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Unknown name fails before any destructive call
- **WHEN** the argument resolves to zero VMs on the cluster
- **THEN** the command SHALL exit non-zero with an error naming the missing VM
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Numeric argument is treated as VMID
- **WHEN** `pmox delete 104` is invoked
- **THEN** the command SHALL resolve `104` as a VMID, not a name
- **AND** the resolver SHALL look up the VM's node via `ClusterResources` so the destroy targets the correct node

### Requirement: Name↔VMID resolver (shared helper)

The `internal/vm` package SHALL expose `Resolve(ctx, client, arg)` returning a VM reference containing VMID, Node, Name, and raw Tags string. The resolver SHALL accept either a numeric VMID or a VM name, and SHALL derive the node from a single call to `ClusterResources(ctx, "vm")`.

#### Scenario: Numeric argument resolves by VMID
- **WHEN** `Resolve(ctx, c, "104")` is called and exactly one VM on the cluster has VMID 104
- **THEN** the resolver SHALL return that VM's node, name, and tags

#### Scenario: Name argument with single match
- **WHEN** `Resolve(ctx, c, "web1")` is called and exactly one VM is named `web1`
- **THEN** the resolver SHALL return that VM's VMID, node, and tags

#### Scenario: Name argument with multiple matches
- **WHEN** `Resolve(ctx, c, "web1")` is called and two VMs are named `web1` with VMIDs 104 and 107
- **THEN** the resolver SHALL return an error containing `multiple VMs named "web1"` and both VMIDs in ascending order

#### Scenario: Name argument with no matches
- **WHEN** `Resolve(ctx, c, "ghost")` is called and no VM is named `ghost`
- **THEN** the resolver SHALL return an error containing `VM "ghost" not found`

### Requirement: `pmox` tag detection

The `internal/vm` package SHALL expose `HasPMOXTag(tagsRaw string) bool`. It SHALL treat both `;` and `,` as tag separators (PVE has varied between the two across versions) and SHALL match the tag case-insensitively against the literal string `pmox`.

#### Scenario: Exact tag present
- **WHEN** `HasPMOXTag("pmox")` is called
- **THEN** it SHALL return true

#### Scenario: Tag present among others with semicolon separator
- **WHEN** `HasPMOXTag("foo;pmox;bar")` is called
- **THEN** it SHALL return true

#### Scenario: Tag present among others with comma separator
- **WHEN** `HasPMOXTag("foo,pmox,bar")` is called
- **THEN** it SHALL return true

#### Scenario: Tag is case-insensitive
- **WHEN** `HasPMOXTag("PMOX")` is called
- **THEN** it SHALL return true

#### Scenario: Substring matches do not count
- **WHEN** `HasPMOXTag("pmoxish")` is called
- **THEN** it SHALL return false

#### Scenario: Empty tags
- **WHEN** `HasPMOXTag("")` is called
- **THEN** it SHALL return false
