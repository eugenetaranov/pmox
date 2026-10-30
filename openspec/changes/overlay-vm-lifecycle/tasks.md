# Tasks

## 1. Start waits for the overlay

- [x] 1.1 Add `vmwait.WaitForOverlayIP(ctx, client, node, vmid, timeout)`. It polls `AgentNetwork` until a `tailscale*`/`wt*` IPv4 appears, and returns `""` on timeout. Verify with httptest cases: appears on the third poll; never appears → `""` after the timeout.
- [x] 1.2 In `getOrStartVM` (`cmd/pmox/ssh.go`), when `client.PreferOverlay` is set, call it after `WaitForIP`, run `WaitForSSH` on the overlay address, and fall back to the LAN address with a note. Verify with an ssh_test seam test for both paths, and that `lan` mode makes no extra calls.

## 2. list and vm info

- [x] 2.1 Carry the overlay kind (`tailscale`/`netbird`/none) from `fetchIPs` into `vm.Row`. Render the ` (ts)`/` (nb)` suffix and add JSON `overlay`. Verify with table and JSON tests in `internal/vm`.
- [x] 2.2 In `pmox vm info`, print the address pmox connects to and its kind. Verify with an info test that has `ens18` plus `tailscale0` under `address: overlay`.

## 3. Docs

- [x] 3.1 `docs/remote-access.md`:
  - Tailscale on the node with `--advertise-routes=<node ip>/32`: the URL stays the same, and it reaches the node even on a café network using the home subnet.
  - Auto-start waiting for the overlay address.
  - A deleted VM's device stays in the tailnet: remove it in the admin console, or join throwaway VMs with ephemeral keys.
  - The `(ts)`/`(nb)` markers.

  Verify that the doc check passes.

## 4. Integration check

- [x] 4.1 Run `task test` and `golangci-lint run --timeout=3m`. Both must pass.
- [ ] 4.2 Manual check:
  - From a phone hotspot, with `p0` advertising `192.168.0.185/32`: `pmox list` works, and `pmox shell` on a stopped VM that has joined starts it and connects over `100.x`.

## Workflow follow-up

- Archive after merge and release.
