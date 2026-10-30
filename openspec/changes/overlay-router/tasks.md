# Tasks

## 1. Spike on the user's PVE (before building)

- [x] 1.1 On a throwaway container, created with a token, confirm four things:
  - `features=nesting=1` is accepted at create
  - `GET /nodes/{node}/lxc/{id}/interfaces` returns `eth0`'s address
  - `pct exec <id> -- /bin/sh -c 'umask 077; cat > /run/k'` over plain `ssh` (no `-t`) receives stdin and returns the command's exit code
  - Tailscale's userspace mode routes a /32 to the node

  Record the results in design.md and adjust the decisions if any fail.

## 2. Containers out of VM commands

- [x] 2.1 Add `Type` to `pveclient.Resource` and `IsQEMU()`. Verify with a cluster-resources decoding test that has `qemu` and `lxc` entries.
- [x] 2.2 Filter to QEMU in `internal/vm/resolve.go`, `internal/vm/pick.go`, and in `cmd/pmox/` `list.go`, `target_prompt.go`, `access.go`, `yardo_fleet.go`, `launch_template.go`, `template_list.go` and `hostinfo.go`. Keep `internal/template/vmid.go` and cleanup's VMID set unfiltered. Verify with tests:
  - `pmox vm info 110` on container 110 → not found
  - `list --all` leaves the container out
  - VMID reservation still sees it

## 3. Node SSH can run commands

- [x] 3.1 Add `pvessh.Client.Run(ctx, cmd, stdin, stdout, stderr)`, with no terminal, the exit code readable from the error, and the session closed when ctx is cancelled. Verify with an in-process x/crypto/ssh test server: stdin is echoed, exit code 3 comes back, and cancellation returns promptly.
- [x] 3.2 Add a `nodeRunFn` seam in `cmd/pmox` that dials node SSH the way the snippet upload does. Verify that a fake records the command and stdin.

## 4. PVE API for containers

- [x] 4.1 In `internal/pveclient` (a new `lxc.go`), add:
  - `CreateLXC`, `LXCStart`, `LXCStop`, `LXCStatus`, `LXCConfig`, `DeleteLXC` (purge, destroy unreferenced disks)
  - `SetLXCConfig` (to clear protection)
  - `LXCInterfaces`
  - `AplInfo`, `DownloadAppliance`
  - `StoragesWithContent`
  - the bridge address from `/nodes/{node}/network`
  - `ClusterFirewallEnabled`
  - the guest firewall options and rules

  Verify with pvetest tests per endpoint (the parameters sent, the responses decoded).
- [x] 4.2 Add template selection: the newest `ubuntu-YY.04-standard` (even `YY`), falling back to the newest `debian-*-standard`. Verify with a table test over sample aplinfo lists (22.04, 24.04, 25.10 → 24.04; no Ubuntu → Debian).

## 5. Router package

- [x] 5.1 Add `internal/router`:
  - `Find` (by tag, lxc only; zero, one or many)
  - `Create` (template, storage, VMID, net0 with a fixed MAC, start, then the setup script via run)
  - `Join` (the key over stdin into a tmpfs file, `tailscale up --reset` with the spec's flags, the file removed)
  - `Status` (API plus `tailscale status --json` and `debug prefs` parsing)
  - `Delete` (logout, clear protection, stop, destroy)

  Verify with unit tests on fakes, including that the key never appears in any command string, and with status JSON fixtures (Running/NeedsLogin, approved/awaiting, route mismatch).
- [x] 5.2 Add node-address detection for the /32: the bridge address, with a fallback to the resolved API host; refuse a non-private or 100.64/10 address; `--route` overrides. Verify with a table test.
- [x] 5.3 Add the opt-in firewall: the datacenter-firewall pre-check, `--ip` required, and the guest options and outbound rules. Verify that tests cover the refusal when the datacenter firewall is off, and that the rules are written when it's on.

## 6. Commands

- [x] 6.1 Add `cmd/pmox/router.go`:
  - `router create` (`--auth-key-file`, `TS_AUTHKEY`, a hidden prompt, `--route`, `--ip`, `--firewall`)
  - `router login`, which streams output and prints the URL
  - `router status`, with `--output json`
  - `router delete`/`rm`, with confirmation, `--yes`, and the at-home warning

  Each fails early without node SSH. Verify with command tests on seams, covering the non-TTY "no key → run router login" path.
- [x] 6.2 Register `router` in `cmd/pmox/tree.go`, and in the command-tree test and help groups. Verify that the command-tree test passes and that `pmox --help` lists it under Resources.

## 7. Doctor

- [x] 7.1 Add router checks to the Network group (running, onboot, Tailscale Running, route approved and matching the node). Extend the unreachable hint with the router-down-at-home lockout and `tailscale set --accept-routes=false`. Verify with doctor tests on fakes.

## 8. Docs and scope note

- [x] 8.1 Update `docs/remote-access.md`:
  - `pmox router` replaces the manual node steps
  - the Tailscale policy snippet (tagOwners, an autoApprover for the /32, a grant to tcp 8006/22 via `tag:pmox-router`)
  - the key recommendation (tagged, pre-approved, single-use, non-ephemeral)
  - the home lockout and its fix
  - the opt-in firewall and its lockout caveat
  - SNAT hiding the source

  Also update the README command list, llms.txt, and the LXC note in `openspec/config.yaml`. Verify that the doc check passes.

## 9. Integration check

- [x] 9.1 Run `task test` and `golangci-lint run --timeout=3m`. Both must pass.
- [ ] 9.2 Manual check on p0:
  - `pmox router create --auth-key-file …` builds a working router
  - `router status` shows the route approved
  - from a phone hotspot, `pmox list` works
  - `router delete` removes the CT and the tailnet device
  - doctor names the lockout while the router is stopped at home

## Workflow follow-up

- Release, then archive after the manual check.
