# Spec Delta

## ADDED Requirements

### Requirement: route removal is named delete
The verb that stops advertising routes SHALL be `pmox router route delete <cidr>...`, with `rm` as its alias, matching every other group. Its behaviour, including `--force` for the node's own route, SHALL be unchanged.

#### Scenario: Canonical name in help
- **WHEN** a user runs `pmox router route --help`
- **THEN** the verb is listed as `delete`

#### Scenario: Alias
- **WHEN** a user runs `pmox router route rm 192.168.0.171/32`
- **THEN** the route is no longer advertised
