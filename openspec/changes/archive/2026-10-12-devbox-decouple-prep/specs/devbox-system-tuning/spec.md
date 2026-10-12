## REMOVED Requirements

### Requirement: Host-synced clock
**Reason**: The KVM PTP clock and snapshot-rollback stepping are Proxmox-specific; they move to pmox's launch cloud-init (`vm-clock-sync`) so devbox-setup stays generic.
**Migration**: New VMs get it at launch by default. Saved answers containing `chrony` skip it with a note; existing VMs keep their chrony config.
