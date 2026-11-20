# Spec Delta

## ADDED Requirements

### Requirement: API tokens are opt-in
The `api-token` category SHALL be treated as destructive: unchecked in the interactive checklist, left out of `--apply` and of the dry-run total unless named with `--include api-token` or `--only api-token`. Its label SHALL say that a listed token may be in use by another machine or person.

#### Scenario: Apply leaves tokens alone
- **WHEN** the cluster holds `root@pam!pmox-alice`, which this machine's config does not use, and `pmox cleanup --apply` runs
- **THEN** the token is not deleted

#### Scenario: Explicit include removes them
- **WHEN** `pmox cleanup --include api-token --apply` runs
- **THEN** the listed tokens are deleted

#### Scenario: Checklist starts unchecked
- **WHEN** `pmox cleanup` runs on a terminal and finds such tokens
- **THEN** the `api-token` row shows its count and is not checked

### Requirement: Help lists every category
`pmox cleanup --help` SHALL list every category the command checks, with the same keys `--only`, `--skip` and `--include` accept, and SHALL mark each opt-in category. The number of opt-in categories SHALL NOT be stated as a fixed count in prose.

#### Scenario: Help and behaviour agree
- **WHEN** a category is added to cleanup without a line in `--help`
- **THEN** a test fails

### Requirement: Known-hosts pins are counted individually
The known-hosts group heading and the dry-run total SHALL count stale pins, not report lines. More than ten pins SHALL be shown as the first ten followed by "and N more".

#### Scenario: Many stale pins
- **WHEN** 43 stale pins are found
- **THEN** the heading reads `Stale known_hosts pins (43)`
- **AND** ten addresses are listed, followed by `and 33 more`
