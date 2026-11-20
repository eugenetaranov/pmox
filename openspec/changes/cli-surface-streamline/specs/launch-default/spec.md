# Spec Delta

## ADDED Requirements

### Requirement: Fractional memory
`--mem` on `pmox launch` and `pmox vm clone`, and the interactive memory prompt, SHALL accept a whole or fractional number of GiB (for example `1.5`), rounded to a whole number of MiB. A value at or below zero, or not a number, SHALL be a user-input error raised before any API call.

#### Scenario: Half a gigabyte more
- **WHEN** a user runs `pmox launch web1 --mem 1.5`
- **THEN** the VM is created with 1536 MiB of memory

#### Scenario: Whole numbers unchanged
- **WHEN** a user runs `pmox launch web1 --mem 4`
- **THEN** the VM is created with 4096 MiB, as before

#### Scenario: Bad value
- **WHEN** a user runs `pmox launch web1 --mem lots`
- **THEN** pmox exits with a user-input error and creates nothing
