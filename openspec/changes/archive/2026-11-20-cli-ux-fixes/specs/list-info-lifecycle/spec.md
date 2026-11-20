# Spec Delta

## ADDED Requirements

### Requirement: list columns stay aligned
`pmox list` SHALL size each column to its widest cell, including an overlay label such as `(tailscale)` after an address, so that every row's cells start in the same column.

#### Scenario: Overlay label in the IP column
- **WHEN** one VM shows `192.168.0.25` and another `100.104.54.92 (tailscale)`
- **THEN** the IP column is as wide as the longer value and no row has trailing text outside a column
