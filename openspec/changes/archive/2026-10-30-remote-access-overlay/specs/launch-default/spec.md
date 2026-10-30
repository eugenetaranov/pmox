# Spec Delta

## MODIFIED Requirements

### Requirement: IP picker heuristic

When `AgentNetwork` reports multiple interfaces, the launcher SHALL
pick one IPv4 address using a deterministic heuristic.

#### Scenario: Virtual interfaces are skipped
- **WHEN** interfaces include `lo`, `docker0`, `br-abc123`, `veth*`, `cni0`, `virbr0`, `tun0`
- **THEN** the picker SHALL exclude those before selecting

#### Scenario: Overlay interfaces are skipped in lan mode
- **WHEN** the server's `address` is `lan` (or unset) and interfaces include `tailscale0` or `wt0` alongside a LAN interface
- **THEN** the picker SHALL exclude the overlay interfaces before selecting, whatever order the guest agent lists them in

#### Scenario: Overlay interfaces are preferred in overlay mode
- **WHEN** the server's `address` is `overlay` and an interface whose name starts with `tailscale` or `wt` has a usable IPv4
- **THEN** the picker SHALL return that address before considering any other interface

#### Scenario: Loopback and link-local addresses are excluded
- **WHEN** the candidate set includes IPv4 addresses in `127.0.0.0/8` or `169.254.0.0/16`
- **THEN** the picker SHALL skip those addresses

#### Scenario: Fallback to first non-loopback non-link-local
- **WHEN** no interface survives the prefix filter
- **THEN** the picker SHALL fall back to the first non-loopback non-link-local IPv4 across all interfaces

#### Scenario: No usable IPv4 returns empty
- **WHEN** no IPv4 passes the heuristic
- **THEN** the picker SHALL return the empty string so the caller can keep polling
