# Spec Delta

## Purpose

Lets pmox create and manage a small router container on the Proxmox bridge that puts the node's API and SSH on an overlay network (Tailscale), so pmox works away from home without changing the hypervisor itself.

## ADDED Requirements

### Requirement: Router identity

The router SHALL be the one LXC container on the cluster tagged `pmox-router`. It SHALL NOT also carry the `pmox` tag. pmox SHALL find it by that tag, keep no local record of it, and refuse to act when more than one container has the tag.

#### Scenario: One router found
- **WHEN** container 110 is tagged `pmox-router`
- **THEN** `pmox router status` SHALL report on container 110

#### Scenario: Two routers
- **WHEN** two containers are tagged `pmox-router`
- **THEN** every `pmox router` command SHALL exit non-zero and list both VMIDs

### Requirement: Router needs node SSH

Every `pmox router` command SHALL require node SSH for the current server, because the router is managed with `pct exec` on the node. Without it, the command SHALL fail before changing anything and say to configure node SSH (`pmox init`).

#### Scenario: No node SSH
- **WHEN** the server has no `node_ssh` and the user runs `pmox router create`
- **THEN** pmox SHALL exit with a user error naming node SSH, and SHALL NOT create anything

### Requirement: Router creation

`pmox router create` SHALL:
- use the newest Ubuntu LTS standard CT template in the node's appliance index (Debian's newest standard template when the index has no Ubuntu LTS), downloading it when it isn't on the node
- create an unprivileged container on the server's bridge: nesting on, a fixed MAC, DHCP unless `--ip` is given, no SSH keys, `onboot=1`, `startup=order=1`, `protection=1`, the `pmox-router` tag
- start it

It SHALL refuse when a router already exists.

#### Scenario: First router
- **WHEN** no router exists and the user runs `pmox router create`
- **THEN** pmox SHALL create and start the container, and continue to the Tailscale join

#### Scenario: Router exists
- **WHEN** a container tagged `pmox-router` exists
- **THEN** `pmox router create` SHALL exit non-zero, naming it and `pmox router status`

#### Scenario: Template chosen
- **WHEN** the appliance index lists `ubuntu-22.04-standard`, `ubuntu-24.04-standard` and `ubuntu-25.10-standard`
- **THEN** create SHALL use `ubuntu-24.04-standard`, the newest LTS (an even `YY.04` release)

#### Scenario: No template storage
- **WHEN** no enabled storage accepts `vztmpl` content
- **THEN** pmox SHALL exit with a user error naming the missing content type

### Requirement: Router has no listening SSH

After creating the container, pmox SHALL disable and remove its SSH server through `pct exec`, so the container listens on no management port.

#### Scenario: sshd removed
- **WHEN** `pmox router create` finishes
- **THEN** `pct exec <id> -- systemctl is-active ssh` SHALL report inactive

### Requirement: Tailscale join with a never-stored key

pmox SHALL install Tailscale in the container in userspace-networking mode. It SHALL join with an auth key from `--auth-key-file`, `TS_AUTHKEY` or a hidden prompt, in that order. The key SHALL reach the container only over the command's stdin, be written to a 0600 tmpfs file that is removed afterwards, and never appear in a command line or pmox's files.

#### Scenario: Key from a file
- **WHEN** the user runs `pmox router create --auth-key-file ~/ts.key`
- **THEN** pmox SHALL run `tailscale up` with that key, and no process argument SHALL contain the key

#### Scenario: No key without a terminal
- **WHEN** there is no key file, no `TS_AUTHKEY` and no terminal
- **THEN** pmox SHALL create the container, skip the join, and say to run `pmox router login`

### Requirement: Advertised route

By default the router SHALL advertise the node's own IPv4 as a /32, taken from the server's bridge address on the node and falling back to the API host's address. It SHALL refuse a node address that is not private (RFC 1918) or is in 100.64.0.0/10. `--route <cidr>`, repeatable, SHALL replace the default. The join SHALL also set `--hostname=pmox-router-<node>`, `--accept-dns=false`, `--accept-routes=false` and `--ssh=false`.

#### Scenario: Node address advertised
- **WHEN** vmbr0 on node `p0` has 192.168.0.185/24
- **THEN** the router SHALL advertise `192.168.0.185/32` as `pmox-router-p0`

#### Scenario: API host is an overlay name
- **WHEN** the bridge has no address and the server URL resolves to 100.101.2.3
- **THEN** create SHALL exit with a user error asking for `--route`

### Requirement: Route validation

Every route pmox advertises (`--route`, `route add`) SHALL be an IPv4 CIDR with no host bits set, inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16, and no wider than /16. Anything else SHALL be refused with a user error.

#### Scenario: Public range refused
- **WHEN** the user runs `pmox router route add 192.0.0.0/3`
- **THEN** pmox SHALL refuse it as not a private range

#### Scenario: Host bits
- **WHEN** the user runs `pmox router route add 192.168.0.185/24`
- **THEN** pmox SHALL refuse it and suggest `192.168.0.0/24`

### Requirement: Managing routes

`pmox router route add <cidr>...` and `pmox router route rm <cidr>...` SHALL change the router's advertised routes in place. They SHALL read its current list, then run `tailscale set --advertise-routes=<new list>` in the container, which keeps it logged in. Removing the node's own /32 SHALL require `--force`. `pmox router route list` SHALL show each advertised route as approved or awaiting approval.

#### Scenario: Add a VM's address
- **WHEN** the router advertises `192.168.0.185/32` and the user runs `pmox router route add 192.168.0.171/32`
- **THEN** the router SHALL advertise both, and pmox SHALL say a new route needs approval unless an auto-approver covers it

#### Scenario: Removing the node route
- **WHEN** the user runs `pmox router route rm 192.168.0.185/32` without `--force`
- **THEN** pmox SHALL refuse, saying the API would be unreachable away from home

#### Scenario: Listing
- **WHEN** `192.168.0.185/32` is approved and `192.168.0.171/32` is not
- **THEN** `pmox router route list` SHALL show the first as approved and the second as awaiting approval

### Requirement: Interactive login

`pmox router login` SHALL run `tailscale up` in the router without a key, with the same flags as create. It SHALL show the login URL as soon as Tailscale prints it, and wait until the router is logged in or the user interrupts.

#### Scenario: Login URL shown
- **WHEN** the user runs `pmox router login`
- **THEN** pmox SHALL print Tailscale's login URL and return once the router's state is Running

### Requirement: Router status

`pmox router status` SHALL report:
- the container's VMID, state, onboot and startup order
- the router's Tailscale state and address
- the advertised route, whether it is approved, and whether it still matches the node's current address
- whether egress is filtered (the guest firewall) or unrestricted

`--output json` SHALL give the same fields.

#### Scenario: Route awaiting approval
- **WHEN** the router advertises 192.168.0.185/32 and its allowed addresses don't include it
- **THEN** status SHALL show the route as awaiting approval in the Tailscale admin console

#### Scenario: Node address changed
- **WHEN** the router advertises 192.168.0.185/32 and the node's bridge now has 192.168.0.190
- **THEN** status SHALL warn that the route no longer matches the node

### Requirement: Router removal

`pmox router delete` SHALL ask for confirmation (or take `--yes`). It SHALL then:
- run `tailscale logout` in the router, so its route stops applying
- clear protection, then stop and destroy the container with its disks
- name the device (`pmox-router-<node>`) to remove in the Tailscale admin console, since logout leaves it listed and a new router would otherwise join under a suffixed name

A failed logout SHALL only warn.

#### Scenario: Clean removal
- **WHEN** the user confirms `pmox router delete`
- **THEN** pmox SHALL log the router out, destroy the container and its disks, and say to remove `pmox-router-p0` in the admin console

#### Scenario: Logout fails
- **WHEN** the router can't reach Tailscale during delete
- **THEN** pmox SHALL warn, destroy the container anyway, and give the same admin-console instruction

### Requirement: Opt-in guest firewall

`pmox router create --firewall` SHALL enable the container's guest firewall with outbound traffic dropped by default. It SHALL allow out only:
- TCP 22 and 8006 to the node
- UDP 41641 and 3478
- TCP 80 and 443
- DNS

It SHALL require `--ip`, and SHALL refuse unless the datacenter firewall is already enabled. pmox SHALL NOT enable the datacenter firewall.

#### Scenario: Datacenter firewall off
- **WHEN** `GET /cluster/firewall/options` reports it disabled and the user passes `--firewall`
- **THEN** create SHALL exit with a user error explaining that guest rules need the datacenter firewall, and that enabling it applies host input rules

#### Scenario: Without the flag
- **WHEN** the user runs create without `--firewall`
- **THEN** the guest firewall SHALL stay off and status SHALL report egress as unrestricted

### Requirement: Doctor reports the router

When a router exists, `pmox doctor` SHALL check it under the Network group:
- the container is running and starts on boot
- Tailscale is Running
- the advertised route is approved and matches the node's address

When the API is unreachable, Tailscale is up locally, and a local interface is on the subnet that contains the node, doctor's hint SHALL name the router-down lockout and `tailscale set --accept-routes=false`.

#### Scenario: Router stopped
- **WHEN** the router container is stopped
- **THEN** doctor SHALL warn and suggest `pmox router status`

#### Scenario: Locked out at home
- **WHEN** the API host 192.168.0.185 is unreachable, the laptop is on 192.168.0.57/24, and Tailscale is up
- **THEN** the `api.reachable` hint SHALL mention the router and `tailscale set --accept-routes=false`
