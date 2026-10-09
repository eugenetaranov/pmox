# vm-access-registry Specification

## Purpose
TBD - created by archiving change vm-access-registry. Update Purpose after archive.
## Requirements
### Requirement: Publish a public key to the cluster registry

`pmox key publish` SHALL upload the caller's configured SSH public key to `/etc/pve/pmox/keys/<name>.pub` on the selected Proxmox clusters, using the node SSH connection pmox already has. `<name>` SHALL default to the caller's local OS username and MAY be overridden with `--name`. pmox SHALL never publish, read or transmit a private key.

#### Scenario: Single configured server
- **WHEN** `bob` runs `pmox key publish` with one server configured
- **THEN** his public key is written to `/etc/pve/pmox/keys/bob.pub` on that cluster
- **AND** the output shows the name, key fingerprint and target host

#### Scenario: Several configured servers
- **WHEN** `pmox key publish` runs on a terminal with more than one server configured
- **THEN** it asks which servers to publish to before writing anything
- **AND** `--context <name>` (one server) or `--all-contexts` selects them without asking

#### Scenario: Name already taken by a different key
- **WHEN** a different key is already published under the same name
- **THEN** both fingerprints are shown and replacing requires confirmation (`--replace` without a terminal)

#### Scenario: Unpublish
- **WHEN** `bob` runs `pmox key unpublish`
- **THEN** `/etc/pve/pmox/keys/bob.pub` is removed from the selected clusters

### Requirement: Desired access state in the registry

The registry SHALL keep the desired grants in `/etc/pve/pmox/access.yaml`. Each published name maps to either "all pmox VMs (including future ones)" or a list of VMIDs. Writes SHALL be atomic and SHALL detect a concurrent modification since the file was read.

#### Scenario: Concurrent edit detected
- **WHEN** `access.yaml` changed on the cluster between pmox reading it and writing it
- **THEN** pmox re-reads and re-applies its change once, and reports a conflict if it changed again

### Requirement: Grant and revoke from the command line

`pmox access grant <vm…> --to <name>` (or `--all-vms`) and `pmox access revoke <vm…> --to <name>` (or `--all-vms`) SHALL update the registry and then apply the change to the affected VMs. `pmox access list [vm]` SHALL show the desired grants and each VM's actual managed keys, marking any differences.

#### Scenario: Grant to a published person
- **WHEN** an admin runs `pmox access grant web1 --to bob` and `bob` has a published key
- **THEN** `access.yaml` lists web1's VMID under bob
- **AND** bob's key is present in web1's managed block

#### Scenario: Grant to an unknown name
- **WHEN** the named person has no published key
- **THEN** the command fails without changing anything and suggests `pmox key publish`

#### Scenario: Revoke
- **WHEN** an admin runs `pmox access revoke web1 --to bob`
- **THEN** bob is removed from web1's grants and his key is removed from web1's managed block

### Requirement: Interactive access setup

`pmox access` with no arguments, on a terminal, SHALL open a full-screen wizard with People, VMs and Review steps:

- **People:** select published names.
- **VMs:** for each selected person, select pmox VMs or "all pmox VMs including future ones", pre-checked with their current grants.
- **Review:** show the additions and removals before applying.

Without a terminal it SHALL print usage and exit with a user-input error.

#### Scenario: Assign two VMs to bob interactively
- **WHEN** the admin selects bob, checks web1 and db1, and confirms Apply on Review
- **THEN** the registry grants bob web1 and db1, and both VMs receive his key
- **AND** the wizard shows a per-VM result for each

#### Scenario: Nothing published yet
- **WHEN** no keys are published on the cluster
- **THEN** the People step explains how to run `pmox key publish` instead of showing an empty list

### Requirement: Guest enforcement through a managed block

pmox SHALL enforce a VM's grants by keeping a block delimited by `# pmox-access begin` and `# pmox-access end` in the login user's `~/.ssh/authorized_keys`. It SHALL read and write the block only through the QEMU guest agent's file-read and file-write API. Lines outside the block SHALL never be changed. pmox SHALL NOT use guest-agent command execution.

#### Scenario: Launch key preserved
- **WHEN** grants are applied to a VM whose authorized_keys holds its cloud-init launch key
- **THEN** that key line is unchanged and the managed block follows it

#### Scenario: Re-applying is idempotent
- **WHEN** a VM's managed block already matches the desired keys
- **THEN** pmox does not rewrite the file and reports it unchanged

#### Scenario: VM cannot be updated now
- **WHEN** the VM is stopped or its guest agent does not respond
- **THEN** it is reported as pending with the reason, and `pmox access sync` applies the grants later

#### Scenario: Missing privilege
- **WHEN** the API token lacks the guest-agent file privileges for the VM
- **THEN** the error names the missing privilege for the cluster's PVE version

### Requirement: Sync

`pmox access sync [vm…|--all]` SHALL bring each VM's managed block in line with the registry and report each VM as updated, unchanged or pending with a reason.

#### Scenario: Catch up after a VM starts
- **WHEN** a VM that was pending because it was stopped is running again and `pmox access sync` runs
- **THEN** its managed block is updated to the desired keys

### Requirement: New VMs receive shared access

After a successful `pmox launch` or `pmox clone`, pmox SHALL apply the registry's grants for the new VM: everyone with all-VM access plus anyone whose list names its VMID. A failure SHALL produce a warning and SHALL NOT fail the command.

#### Scenario: All-VM grantee can reach a new VM
- **WHEN** carol has all-VM access and alice launches web2
- **THEN** carol's key is in web2's managed block without further action

#### Scenario: Registry unavailable at launch
- **WHEN** the registry cannot be read during launch
- **THEN** launch succeeds and prints a warning pointing at `pmox access sync <vm>`

### Requirement: Guidance on SSH key rejection

When an SSH-based pmox command (shell, exec, cp, sync, mount, apply) fails because the guest rejected the caller's key, pmox SHALL print the key path, its fingerprint and the guest user, followed by the publish-then-grant steps. The command SHALL exit with a dedicated authentication exit code.

#### Scenario: Key not published
- **WHEN** bob's `pmox shell web1` is rejected and his key is not published
- **THEN** the message tells him to run `pmox key publish` and have an admin run `pmox access grant web1 --to bob`

#### Scenario: Key published but not granted
- **WHEN** bob's key is published but web1 does not grant him
- **THEN** the message says his key is published but not granted for web1

### Requirement: Doctor and cleanup cover shared access

`pmox doctor` SHALL check whether the token has the guest-agent file privileges and whether the registry is reachable, and SHALL warn about VMs whose managed block differs from the registry. `pmox cleanup` SHALL offer to remove registry grants for VMIDs that no longer exist. Removing published keys SHALL be an opt-in destructive category.

#### Scenario: Drift reported
- **WHEN** a VM's managed block is missing a key the registry grants
- **THEN** doctor warns and suggests `pmox access sync <vm>`

#### Scenario: Grant for a deleted VM
- **WHEN** `access.yaml` lists a VMID that no longer exists
- **THEN** cleanup lists it under the access-grant category and removes it when applied

