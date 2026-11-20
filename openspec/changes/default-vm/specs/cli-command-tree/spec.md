# Spec Delta

## ADDED Requirements

### Requirement: vm use verb
The `vm` group SHALL have the verb `use` (see `default-vm`), listed with the lifecycle verbs in help and in the palette. It SHALL have no top-level shortcut.

#### Scenario: Help lists it
- **WHEN** a user runs `pmox vm --help`
- **THEN** `use` is listed with a summary saying it sets the default VM

#### Scenario: No root shortcut
- **WHEN** a user runs `pmox use web1`
- **THEN** pmox reports an unknown command
