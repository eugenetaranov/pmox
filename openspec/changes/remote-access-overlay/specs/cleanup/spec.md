# Spec Delta

## ADDED Requirements

### Requirement: Every address a running VM reports is live for known_hosts

When cleanup decides which known_hosts pins are stale, it SHALL treat every usable IPv4 that a running VM's guest agent reports as live, on every interface, the overlay included. It SHALL NOT use only the single address the IP picker would choose.

#### Scenario: Overlay pin kept
- **WHEN** a running VM reports `ens18` 10.83.17.20 and `tailscale0` 100.101.7.3, and known_hosts has pins for both
- **THEN** cleanup SHALL list neither pin as stale

#### Scenario: Pin for a gone address still stale
- **WHEN** known_hosts has a pin for an IP that no running VM reports
- **THEN** cleanup SHALL list it as stale, as before

#### Scenario: Unknown VM addresses keep pins safe
- **WHEN** a running VM's guest agent does not answer
- **THEN** cleanup SHALL NOT mark any guest pin stale, as before
