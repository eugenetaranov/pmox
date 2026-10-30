# Spec Delta

## MODIFIED Requirements

### Requirement: Guest agent IP discovery

Both commands SHALL discover the VM's IPv4 address by querying the QEMU guest agent via the PVE API. If the guest agent does not respond or returns no usable IPv4 address, the command SHALL fail with an actionable error message.

#### Scenario: Guest agent returns IP
- **WHEN** the guest agent reports interfaces with a usable IPv4 address
- **THEN** the command SHALL use that IP for the SSH connection

#### Scenario: Server prefers the overlay address
- **WHEN** the server's `address` is `overlay` and the guest agent reports a `tailscale*` or `wt*` interface with a usable IPv4
- **THEN** the command SHALL use the overlay IP for the SSH connection, and SHALL pin the VM's host key under that IP

#### Scenario: Guest agent not responding on a running VM
- **WHEN** the VM is running but the guest agent does not respond
- **THEN** the command SHALL exit non-zero
- **AND** the error SHALL mention `qemu-guest-agent`

#### Scenario: Guest agent returns no IPv4
- **WHEN** the guest agent responds but no interface has a usable IPv4 address
- **THEN** the command SHALL exit non-zero with an error about missing IP
