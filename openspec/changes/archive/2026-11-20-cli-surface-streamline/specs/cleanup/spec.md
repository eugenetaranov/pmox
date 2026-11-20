# Spec Delta

## ADDED Requirements

### Requirement: `--yes` removes without asking
`pmox cleanup` SHALL accept `-y`/`--yes` with the same effect as `--apply`, so that the confirmation bypass is spelled as on every other command. `--apply` SHALL remain supported and documented. `PMOX_ASSUME_YES` SHALL NOT make cleanup remove anything: an environment variable set for other commands must not turn a dry run into a removal.

#### Scenario: Yes flag
- **WHEN** `pmox cleanup --yes` runs
- **THEN** the selected items are removed exactly as with `pmox cleanup --apply`

#### Scenario: Environment variable alone
- **WHEN** `PMOX_ASSUME_YES=1 pmox cleanup` runs without a terminal
- **THEN** it reports what it would remove and removes nothing

### Requirement: One way to include opt-in categories
`--include <categories>` SHALL be the documented way to add opt-in categories. `--include-templates` and `--include-vms` SHALL keep working as deprecated forms of `--include template` and `--include vm` and SHALL NOT appear in help.

#### Scenario: Old shorthand
- **WHEN** `pmox cleanup --include-vms --apply` runs
- **THEN** it behaves as `pmox cleanup --include vm --apply`
- **AND** stderr shows one line naming `--include vm`
