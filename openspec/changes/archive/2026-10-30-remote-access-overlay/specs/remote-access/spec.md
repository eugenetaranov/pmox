# Spec Delta

## Purpose

Lets pmox reach a home Proxmox cluster and its VMs from outside the home LAN over an overlay network (Tailscale or NetBird), keep working when the laptop comes back home, and explain how to connect a laptop's SSH, Docker and MCP clients to a VM.

## ADDED Requirements

### Requirement: Per-server address preference

A server entry in `config.yaml` SHALL accept an optional `address` key with the value `lan` or `overlay`. A missing key SHALL mean `lan`. Any other value SHALL make a command that connects to that server's VMs fail, with an error naming the server and the allowed values. `pmox doctor` SHALL report it like other invalid config.

#### Scenario: Default is lan
- **WHEN** a server has no `address` key
- **THEN** pmox SHALL connect to its VMs exactly as with `address: lan`

#### Scenario: Invalid value
- **WHEN** a server has `address: auto` and the user runs `pmox shell web1`
- **THEN** pmox SHALL exit non-zero with an error naming the server and `lan` and `overlay`

#### Scenario: Invalid value does not break unrelated commands
- **WHEN** a server has `address: auto` and the user runs `pmox config get-contexts`
- **THEN** the command SHALL succeed
- **AND** `pmox doctor` SHALL report the invalid `address`

### Requirement: Overlay address selection

With `address: overlay`, every command that connects to a VM SHALL use the IPv4 on the VM's overlay interface (one whose name starts with `tailscale` or `wt`) when the guest agent reports one, and SHALL otherwise use the address the `lan` heuristic picks. pmox SHALL NOT probe or race several addresses.

#### Scenario: VM on the tailnet
- **WHEN** `address: overlay` is set and the guest agent reports `ens18` 10.83.17.20 and `tailscale0` 100.101.7.3
- **THEN** `pmox shell`, `exec`, `cp`, `sync`, `mount` and `vm ssh-config` SHALL use 100.101.7.3

#### Scenario: NetBird interface
- **WHEN** `address: overlay` is set and the guest agent reports `wt0` 100.88.4.9 alongside a LAN interface
- **THEN** pmox SHALL use 100.88.4.9

#### Scenario: VM not joined yet
- **WHEN** `address: overlay` is set and the VM has no overlay interface
- **THEN** pmox SHALL use the LAN address, as in `lan` mode

#### Scenario: Overlay address unreachable
- **WHEN** `address: overlay` is set and the overlay address does not answer
- **THEN** pmox SHALL fail as it does for any unreachable VM, without trying the LAN address

### Requirement: ssh-config reflects the chosen address

`pmox vm ssh-config` SHALL write the address chosen by the server's `address` setting as the block's `HostName`, so a saved `~/.ssh/config` entry, and any tool built on it, reaches the VM by the same route pmox uses.

#### Scenario: Overlay HostName
- **WHEN** `address: overlay` is set and the VM has a `tailscale0` address
- **THEN** the printed block's `HostName` SHALL be that address

### Requirement: Doctor names off-LAN reachability failures

When the PVE API is unreachable and its host is a private (RFC1918) IPv4 address or resolves to one, `pmox doctor` SHALL say that the machine may be off the home LAN and needs a subnet route, or Tailscale or NetBird on the node.

#### Scenario: Away without a route
- **WHEN** the API host is 10.83.17.2 and the dial times out
- **THEN** the `api.reachable` hint SHALL mention being off the home LAN and enabling a Tailscale or NetBird route to that network

#### Scenario: Public or named host unaffected
- **WHEN** the API host resolves to a public address
- **THEN** the hint SHALL stay as it is today

### Requirement: Doctor detects subnet overlap

When the API is unreachable at a private address and one of this machine's own interfaces is on a subnet that contains that address, `pmox doctor` SHALL say that the current network uses the same addresses as the home LAN.

#### Scenario: Café on the same subnet
- **WHEN** the API host is 192.168.1.10, it is unreachable, and the laptop's Wi-Fi is 192.168.1.57/24
- **THEN** doctor SHALL report that the local network overlaps the home LAN and SHALL suggest renumbering the home LAN

### Requirement: Doctor warns when overlay mode has no overlay

When the current server has `address: overlay` and none of this machine's interfaces has an address in 100.64.0.0/10, `pmox doctor` SHALL report a warning, not a failure, telling the user to bring Tailscale or NetBird up.

#### Scenario: Tailscale down on the laptop
- **WHEN** `address: overlay` is set and the laptop has no 100.64.0.0/10 address
- **THEN** doctor SHALL warn that the overlay is down and SHALL still exit zero if nothing failed

### Requirement: Remote access guide

The repository SHALL ship `docs/remote-access.md`. It SHALL explain how to reach a home cluster from away, and how to connect a laptop to a VM's shell, Docker and MCPJungle gateway, both at home and away. The README and `llms.txt` SHALL link to it.

#### Scenario: Guide covers connecting
- **WHEN** a user reads `docs/remote-access.md`
- **THEN** it SHALL cover:
  - the prerequisites: CGNAT check, an uncommon home subnet, a scoped API token
  - the subnet-router and agent-on-each-machine topologies, and when to set `address: overlay`
  - Tailscale and NetBird setup, and how to check for a direct path at home
  - SSH and `ssh-config`
  - Docker over an SSH context, and over the TLS API
  - the MCPJungle gateway over the overlay with a token, and over an SSH tunnel
  - troubleshooting

#### Scenario: Linked from README and llms.txt
- **WHEN** a user reads the README or `llms.txt`
- **THEN** each SHALL link to `docs/remote-access.md`
