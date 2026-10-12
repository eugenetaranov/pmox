# vm-clock-sync Specification

## Purpose
Keeping a VM's clock in step with its Proxmox host through pmox's own cloud-init (chrony on the KVM PTP clock), with an opt-out.
## Requirements
### Requirement: Host-synced clock at launch
By default, pmox SHALL add to each launched VM's cloud-init: the `chrony` package, `ptp_kvm` loaded at boot, and a first-boot command that installs a chrony drop-in with `refclock PHC /dev/ptp0 poll 2 dpoll -2 offset 0 stratum 2 prefer` and `makestep 1 -1` when `/dev/ptp0` exists, then restarts chrony. When `/dev/ptp0` is absent, chrony SHALL keep its default NTP pools. The user's cloud-init file on disk SHALL NOT be changed.

#### Scenario: After snapshot rollback
- **WHEN** a launched VM resumes from a snapshot with its clock several minutes off
- **THEN** chrony steps the clock to the host time within 15 seconds

#### Scenario: No PTP device
- **WHEN** the VM has no `/dev/ptp0`
- **THEN** chrony runs with its default pools and no pmox drop-in is active

#### Scenario: Existing packages kept
- **WHEN** the user's cloud-init already has `packages: [git, chrony]`
- **THEN** the uploaded user-data lists `chrony` once, and `git` is kept

### Requirement: Clock sync opt-out
`clock_sync: false` in a server's config, or `--no-clock-sync` on `pmox launch`, SHALL leave the user-data without the clock-sync additions.

#### Scenario: Flag
- **WHEN** `pmox launch web1 --no-clock-sync` runs
- **THEN** the uploaded user-data has no chrony package, ptp_kvm entry or pmox chrony drop-in

