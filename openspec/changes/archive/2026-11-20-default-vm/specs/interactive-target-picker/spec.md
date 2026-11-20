# Spec Delta

## MODIFIED Requirements

### Requirement: Shared VM target picker helper

pmox SHALL provide a shared helper that every command taking a single `<name|vmid>` positional argument delegates to when the argument is omitted. The helper SHALL resolve the set of pmox-tagged VMs on the configured cluster via the same `ClusterResources` call used by `pmox list` and apply the following selection rules in order:

0. If the calling command uses the default VM (see `default-vm`), `--pick` was not given, and the context's default VM is in the set, the helper SHALL return it without prompting, regardless of TTY state. If a default is stored but is not in the set, the helper SHALL report and clear it as `default-vm` requires and continue with the rules below.
1. If the set is empty, the helper SHALL return an error whose message states that no pmox VMs were found and suggests `pmox launch`.
2. If the set contains exactly one VM, the helper SHALL return that VM without prompting, regardless of TTY state.
3. If the set contains two or more VMs AND both stdin and stderr are connected to a terminal, the helper SHALL display an interactive picker listing every VM with columns matching `pmox list` (name, vmid, node, status, IPv4). The user SHALL select one VM using arrow keys. On confirmation the helper SHALL return the selected VM.
4. If the set contains two or more VMs AND stdin or stderr is not a terminal, the helper SHALL return an error equivalent in message to Cobra's default "accepts 1 arg(s)" validation failure so that scripts behave the same as they did before this change.

#### Scenario: Default VM is set
- **WHEN** a command that uses the default is invoked with no positional argument
- **AND** the context's default VM is among the pmox-tagged VMs
- **THEN** the helper SHALL return it
- **AND** SHALL NOT display any interactive UI

#### Scenario: Default VM is set but the command does not use it
- **WHEN** `pmox stop` is invoked with no positional argument and a default VM is set
- **THEN** rules 1–4 SHALL apply as if no default were set

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

## ADDED Requirements

### Requirement: The picker shows which VM is the default
When a default VM is set, the single-select and multi-select pickers SHALL mark its row. The single-select picker SHALL open with the cursor on it; the multi-select picker SHALL NOT pre-select it.

#### Scenario: Picker opened with --pick
- **WHEN** `web1` is the default and `pmox shell --pick` shows the picker
- **THEN** the `web1` row is marked as the default and the cursor starts on it
