## MODIFIED Requirements

### Requirement: `pmox delete` command

The CLI SHALL expose `pmox delete <name|vmid>` which resolves the argument to a single VM on the configured cluster, stops it if running, and then destroys it via `DELETE /nodes/{node}/qemu/{vmid}`. The command SHALL wait for every underlying PVE task to reach a terminal state before returning.

The command SHALL refuse to destroy a VM whose tags do not contain `pmox` unless the user passes `--force`. The error message SHALL name the VM, state that it is not tagged `pmox`, and suggest `--force` as the bypass.

The command SHALL require an interactive yes/no confirmation before issuing any `Shutdown`, `Stop`, or `Delete` API call. The confirmation SHALL be skipped only when `--yes` / `-y` is passed or when the `PMOX_ASSUME_YES` environment variable is truthy. When stdin is not a TTY and no bypass is set, the command SHALL exit non-zero with an error directing the user to pass `--yes` for non-interactive use, and SHALL NOT issue any destructive API call.

The confirmation prompt SHALL print a one-line summary of the resolved VM (name, VMID, node, tags) before asking, so the operator can verify what is about to be destroyed. When `--force` is in effect, the prompt SHALL additionally state that the tag check is bypassed and that hard stop will be used.

`--yes` and `--force` SHALL be orthogonal: `--yes` only skips the confirmation prompt; `--force` only changes tag-check and stop-verb behavior. The combination `--yes --force` is the explicit non-interactive force-delete path.

#### Scenario: Tag check blocks untagged VMs
- **WHEN** `pmox delete web1` is invoked against a VM whose tags do not contain `pmox`
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL contain `not tagged "pmox"` and the VM's name or VMID
- **AND** the error SHALL mention `--force`
- **AND** the command SHALL NOT issue any `Shutdown`, `Stop`, or `Delete` API call
- **AND** the command SHALL NOT prompt for confirmation

#### Scenario: `--force` bypasses the tag check
- **WHEN** `pmox delete --force web1` is invoked against an untagged VM
- **THEN** the command SHALL still prompt for confirmation
- **AND** on `y` SHALL proceed with the stop + destroy sequence

#### Scenario: Confirmation prompt blocks destructive calls until approved
- **WHEN** `pmox delete web1` is invoked interactively against a pmox-tagged running VM
- **AND** the operator answers `n` (or presses enter to take the default)
- **THEN** the command SHALL exit non-zero with a "cancelled" message
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Confirmation prompt approved
- **WHEN** `pmox delete web1` is invoked interactively against a pmox-tagged running VM
- **AND** the operator answers `y`
- **THEN** the command SHALL proceed with the existing shutdown + destroy sequence

#### Scenario: `--yes` skips the prompt
- **WHEN** `pmox delete --yes web1` is invoked against a pmox-tagged VM
- **THEN** the command SHALL NOT read from stdin
- **AND** SHALL proceed directly to the shutdown + destroy sequence

#### Scenario: `PMOX_ASSUME_YES=1` skips the prompt
- **WHEN** `pmox delete web1` is invoked with `PMOX_ASSUME_YES=1` in the environment
- **THEN** the command SHALL behave as if `--yes` had been passed

#### Scenario: Non-TTY stdin without `--yes` is a hard failure
- **WHEN** `pmox delete web1` is invoked with stdin connected to a pipe (not a terminal) and `--yes` / `PMOX_ASSUME_YES` are unset
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL mention `--yes` and `PMOX_ASSUME_YES`
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Confirmation summary names the VM
- **WHEN** the command is about to prompt
- **THEN** the prompt SHALL include the VM's name, VMID, node, and tags
- **AND** the prompt's default answer SHALL be `No` (`[y/N]`)

#### Scenario: `--force` prompt warns about hard stop and tag bypass
- **WHEN** `pmox delete --force web1` reaches the prompt
- **THEN** the printed summary SHALL state that hard stop will be used
- **AND** SHALL state that the pmox tag check is bypassed

#### Scenario: Running VM is shut down before destroy
- **WHEN** `pmox delete --yes web1` is invoked against a VM whose status is `running`
- **THEN** the command SHALL issue `Shutdown`, `WaitTask`, `Delete`, `WaitTask` in that order against the resolved node and VMID

#### Scenario: Stopped VM skips shutdown
- **WHEN** `pmox delete --yes web1` is invoked against a VM whose status is `stopped`
- **THEN** the command SHALL issue `Delete` and `WaitTask` only
- **AND** the command SHALL NOT issue `Shutdown` or `Stop`

#### Scenario: `--force` uses hard stop instead of shutdown
- **WHEN** `pmox delete --yes --force web1` is invoked against a running VM
- **THEN** the stop phase SHALL issue `Stop` (POST `/status/stop`), not `Shutdown`

#### Scenario: Already-gone VM is treated as success
- **WHEN** the initial `GetStatus` call returns an error wrapping `ErrNotFound`
- **THEN** the command SHALL print a note to stderr stating the VM is already gone
- **AND** the command SHALL exit 0
- **AND** the command SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Ambiguous name fails before any destructive call
- **WHEN** two or more VMs on the cluster share the name passed to `pmox delete`
- **THEN** the command SHALL exit non-zero with an error listing the matching VMIDs
- **AND** the command SHALL NOT prompt and SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Unknown name fails before any destructive call
- **WHEN** the argument resolves to zero VMs on the cluster
- **THEN** the command SHALL exit non-zero with an error naming the missing VM
- **AND** the command SHALL NOT prompt and SHALL NOT issue `Shutdown`, `Stop`, or `Delete`

#### Scenario: Numeric argument is treated as VMID
- **WHEN** `pmox delete 104` is invoked
- **THEN** the command SHALL resolve `104` as a VMID, not a name
- **AND** the resolver SHALL look up the VM's node via `ClusterResources` so the destroy targets the correct node
