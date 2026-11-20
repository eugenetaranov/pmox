# Spec Delta

## ADDED Requirements

### Requirement: restart verb and shortcut
The `vm` group SHALL have the verb `restart`, listed with the lifecycle verbs, and pmox SHALL provide the top-level shortcut `pmox restart` under the Common help section, with the same flag and output parity as the other shortcuts.

#### Scenario: Shortcut and canonical are equivalent
- **WHEN** `pmox restart web1` and `pmox vm restart web1` run against VMs in the same state
- **THEN** both perform the same calls and print the same output

#### Scenario: Typo suggestion
- **WHEN** a user runs `pmox restrat web1`
- **THEN** pmox suggests `restart`
