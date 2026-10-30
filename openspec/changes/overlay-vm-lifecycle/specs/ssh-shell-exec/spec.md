# Spec Delta

## ADDED Requirements

### Requirement: Auto-start waits for the overlay address

When `address: overlay` is set and `shell` or `exec` starts a stopped VM, the command SHALL wait (up to 90 seconds after the VM has an IP) for the guest agent to report an IPv4 on a `tailscale*` or `wt*` interface. It SHALL then wait for SSH on that address. If none appears in time, it SHALL use the LAN address and print a note saying so.

#### Scenario: VM rejoins the tailnet after boot
- **WHEN** `address: overlay` is set, `web1` is stopped, and its `tailscale0` address appears 20 seconds after boot
- **THEN** `pmox shell web1` SHALL connect to the `tailscale0` address

#### Scenario: VM never joins
- **WHEN** `address: overlay` is set and the started VM reports no overlay interface within the wait
- **THEN** the command SHALL try the LAN address and print a note that the VM has no overlay address

#### Scenario: lan mode unchanged
- **WHEN** `address` is `lan` and `shell` starts a stopped VM
- **THEN** the command SHALL wait on the LAN address only, as before
