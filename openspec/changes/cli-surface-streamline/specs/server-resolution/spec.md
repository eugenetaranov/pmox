# Spec Delta

## MODIFIED Requirements

### Requirement: --server flag and PMOX_SERVER env var

The `pmox` root command SHALL accept a persistent `--server` flag and
the resolver SHALL honor the `PMOX_SERVER` environment variable. Both
are deprecated in favour of `--context` and `PMOX_CONTEXT`: they SHALL
keep resolving as before, SHALL be hidden from help and completion,
and SHALL cause one stderr line naming the replacement.

#### Scenario: --server is a persistent root flag
- **WHEN** the user runs `pmox --help`
- **THEN** the `--server` flag SHALL NOT appear in the global flags list
- **AND** any subcommand SHALL still accept `--server` without redeclaring it

#### Scenario: --server prints a note
- **WHEN** the user runs `pmox list --server lab`
- **THEN** the `lab` context SHALL be listed
- **AND** stderr SHALL show one line pointing at `--context`

#### Scenario: configure ignores --server and PMOX_SERVER
- **WHEN** the user runs `pmox configure --server https://foo`
- **OR** `pmox configure` with `PMOX_SERVER` set
- **THEN** `configure` SHALL proceed with its interactive flow without consulting either value
- **AND** SHALL NOT error on the presence of the flag or env var

## ADDED Requirements

### Requirement: --context accepts a name or a URL
`--context` and `PMOX_CONTEXT` SHALL accept either a context name or a server URL in any form the canonicalization rules accept, and SHALL resolve to the same server `--server` would have. When both `--context` and `--server` are given and name different servers, pmox SHALL fail with a user-input error.

#### Scenario: Context by URL
- **WHEN** a context `lab` is configured for `https://192.168.0.185:8006` and a user runs `pmox list --context 192.168.0.185`
- **THEN** the `lab` context is used

#### Scenario: Conflicting flags
- **WHEN** a user runs `pmox list --context lab --server prod`
- **THEN** pmox exits with a user-input error naming both values
