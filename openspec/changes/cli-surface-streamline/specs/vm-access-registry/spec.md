# Spec Delta

## ADDED Requirements

### Requirement: revoke names the person with `--from`
`pmox access revoke [vm...] --from <name>` SHALL remove that person's access. `--to` SHALL keep working on `revoke` as a deprecated spelling and SHALL NOT appear in its help. `grant` keeps `--to`.

#### Scenario: New spelling
- **WHEN** a user runs `pmox access revoke web1 --from bob`
- **THEN** bob's access to `web1` is removed

#### Scenario: Old spelling
- **WHEN** a user runs `pmox access revoke web1 --to bob`
- **THEN** bob's access to `web1` is removed and stderr shows one line naming `--from`

#### Scenario: Both given
- **WHEN** a user runs `pmox access revoke web1 --from bob --to carol`
- **THEN** pmox exits with a user-input error
