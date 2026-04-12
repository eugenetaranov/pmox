## ADDED Requirements

### Requirement: Shared VM target picker helper

pmox SHALL provide a shared helper that every command taking a single `<name|vmid>` positional argument delegates to when the argument is omitted. The helper SHALL resolve the set of pmox-tagged VMs on the configured cluster via the same `ClusterResources` call used by `pmox list` and apply the following selection rules in order:

1. If the set is empty, the helper SHALL return an error whose message states that no pmox VMs were found and suggests `pmox launch`.
2. If the set contains exactly one VM, the helper SHALL return that VM without prompting, regardless of TTY state.
3. If the set contains two or more VMs AND both stdin and stderr are connected to a terminal, the helper SHALL display an interactive picker listing every VM with columns matching `pmox list` (name, vmid, node, status, IPv4). The user SHALL select one VM using arrow keys. On confirmation the helper SHALL return the selected VM.
4. If the set contains two or more VMs AND stdin or stderr is not a terminal, the helper SHALL return an error equivalent in message to Cobra's default "accepts 1 arg(s)" validation failure so that scripts behave the same as they did before this change.

#### Scenario: Exactly one pmox VM exists
- **WHEN** a single-target command is invoked with no positional argument
- **AND** `ClusterResources` reports exactly one pmox-tagged VM
- **THEN** the helper SHALL return that VM as the resolved target
- **AND** SHALL NOT display any interactive UI
- **AND** SHALL NOT read from stdin

#### Scenario: Multiple pmox VMs, interactive terminal
- **WHEN** a single-target command is invoked with no positional argument
- **AND** `ClusterResources` reports two or more pmox-tagged VMs
- **AND** stdin and stderr are both TTYs
- **THEN** the helper SHALL display an arrow-key picker populated with those VMs
- **AND** the label for each row SHALL include name, vmid, node, status, and IPv4 when available
- **AND** the helper SHALL return the VM the user selects

#### Scenario: Multiple pmox VMs, non-interactive shell
- **WHEN** a single-target command is invoked with no positional argument
- **AND** `ClusterResources` reports two or more pmox-tagged VMs
- **AND** stdin is not a TTY (e.g., piped) or stderr is not a TTY
- **THEN** the helper SHALL exit non-zero without prompting
- **AND** the error SHALL match the existing "missing argument" behavior

#### Scenario: Zero pmox VMs on the cluster
- **WHEN** a single-target command is invoked with no positional argument
- **AND** `ClusterResources` reports zero pmox-tagged VMs
- **THEN** the helper SHALL exit non-zero
- **AND** the error SHALL state that no pmox VMs were found
- **AND** the error SHALL suggest `pmox launch`

#### Scenario: User aborts the picker
- **WHEN** the picker is displayed and the user presses Ctrl+C or Escape
- **THEN** the helper SHALL cancel the command's context (via SIGINT)
- **AND** pmox SHALL exit non-zero without issuing any API call beyond the initial `ClusterResources` fetch

### Requirement: Picker does not change `--force` or tag semantics

The picker SHALL list only pmox-tagged VMs, matching the set that `pmox list` shows today. `--force` SHALL NOT expand the picker's set to untagged VMs. Users who need to target an untagged VM SHALL continue to type the name or VMID explicitly.

#### Scenario: `--force` with no target still requires explicit name
- **WHEN** a single-target command is invoked with `--force` but no positional argument
- **AND** stdin and stderr are both TTYs
- **THEN** the picker SHALL show only pmox-tagged VMs, not untagged ones
- **AND** targeting an untagged VM SHALL still require typing its name or VMID

### Requirement: Explicit target bypasses the picker entirely

When a positional `<name|vmid>` argument is supplied, the picker SHALL NOT run. The command SHALL resolve the argument via the existing `vm.Resolve` path and SHALL preserve every existing error condition (ambiguous name, unknown name, non-pmox tag without `--force`).

#### Scenario: Explicit name
- **WHEN** `pmox shell web1` is invoked with `web1` as a positional argument
- **THEN** the command SHALL call `vm.Resolve("web1")` without consulting the picker
- **AND** SHALL NOT call `ClusterResources` for the picker's benefit (the existing resolver call is unchanged)

#### Scenario: Explicit VMID
- **WHEN** `pmox delete 104` is invoked
- **THEN** the command SHALL resolve `104` as a VMID exactly as it does today
- **AND** the picker SHALL NOT be displayed
