# Spec Delta

## ADDED Requirements

### Requirement: VM commands consider QEMU VMs only

Name/VMID resolution, the interactive VM picker, `pmox list`, `access`, the `yardo` fleet commands, template lookups and `pmox info`'s VM counts SHALL consider only QEMU guests. LXC containers on the cluster SHALL NOT resolve as VMs. VMID allocation and cleanup's view of existing guests SHALL still include containers.

#### Scenario: Container VMID
- **WHEN** container 110 exists and the user runs `pmox vm info 110`
- **THEN** pmox SHALL report that no VM 110 was found, rather than an API error from a QEMU call

#### Scenario: list --all
- **WHEN** the cluster has VM 104 and container 110
- **THEN** `pmox list --all` SHALL list VM 104 and not container 110

#### Scenario: VMID allocation sees containers
- **WHEN** pmox picks a VMID for a new template and container 9000 exists
- **THEN** it SHALL NOT pick 9000
