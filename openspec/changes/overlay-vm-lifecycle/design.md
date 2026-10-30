# Design

## Context

See proposal.md. The relevant code:
- `getOrStartVM` (`cmd/pmox/ssh.go`) starts a stopped VM, then calls `vmwait.WaitForIP` and `WaitForSSH` on the LAN address.
- `list` fills `Row.IP` through `PickGuestIPv4`.

## Goals / Non-Goals

**Goals:** a stopped VM can be started and reached from away; the address pmox uses is visible.

**Non-Goals:**
- joining VMs to a tailnet (yardo's job)
- the Tailscale or NetBird APIs, or any stored token
- reaching the PVE API (a /32 route on the node; docs only)
- removing a deleted VM's device from the tailnet. `yardo forget` covers joins yardo made. For the rest, use ephemeral keys or the admin console; revisit if it becomes a problem.

## Decisions

**1. Wait for the overlay, then fall back.**
- A started VM gets its LAN address from DHCP first. `tailscaled` comes up a few seconds later; it stays logged in across reboots.
- In overlay mode, after `WaitForIP`, poll the guest agent until `PickGuestIPv4(…, true)` returns an overlay-interface address (a new `vmwait.WaitForOverlayIP`, 90s), then `WaitForSSH` on it.
- On timeout, use the LAN address with a note. That works at home, and fails clearly away, as today.
- *Alternative:* fail on timeout. Rejected, because at home the LAN address is still a good answer.

**2. Marking in list, not a new column.**
- The fixed five-column table stays as it is.
- The suffix needs the interface name, so `fetchIPs` keeps it next to the IP (`Row.Overlay`).

## Risks / Trade-offs

- **[Risk]** `shell` on a stopped VM with Tailscale logged out waits 90s before falling back. → The note names the cause; `sudo yardo` (Logins & keys) or `sudo tailscale up` fixes it.

## Migration Plan

Nothing to migrate: the wait applies only under `address: overlay`, and the list and info markers only add information.

## Open Questions

- **Host-key alignment with yardo.** pmox's `ssh-config` names the VM by its overlay IP and uses pmox's own `known_hosts_guests`. `yardo connect` prints Host = short name, HostName = MagicDNS FQDN, and HostKeyAlias = short name, against the default `~/.ssh/known_hosts`. Pins differ only when a user keeps both blocks. Adding `HostKeyAlias <vm name>` to pmox's block would make pmox's pins name-keyed; that's deferred, since it would change how pmox's own SSH pins are keyed.

