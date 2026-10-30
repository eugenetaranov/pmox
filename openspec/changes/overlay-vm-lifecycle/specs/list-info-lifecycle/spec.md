# Spec Delta

## ADDED Requirements

### Requirement: list marks overlay addresses

When the address `pmox list` shows for a VM is on a `tailscale*` interface, the table SHALL suffix it with ` (ts)`; on a `wt*` interface, with ` (nb)`. JSON output SHALL keep the plain address in `ip` and SHALL add `"overlay": "tailscale"` or `"overlay": "netbird"`.

#### Scenario: Overlay VM in the table
- **WHEN** `address: overlay` is set and `payzoff` reports `tailscale0` 100.104.54.92
- **THEN** its IP cell SHALL read `100.104.54.92 (ts)`

#### Scenario: LAN address unmarked
- **WHEN** the address shown is on `ens18`
- **THEN** the cell SHALL show the address alone

### Requirement: vm info names the connect address

`pmox vm info` SHALL state which address pmox connects to for the VM under the server's `address` setting, alongside the interfaces it already lists.

#### Scenario: Overlay connect address
- **WHEN** `address: overlay` is set and the VM has `ens18` 192.168.0.171 and `tailscale0` 100.104.54.92
- **THEN** the output SHALL say pmox connects to 100.104.54.92 (Tailscale)
