# Tasks

## 1. Config: `address` setting

- [x] 1.1 Add `Address string \`yaml:"address,omitempty"\`` to `Server` in `internal/config/config.go`, with an accessor that returns `lan` when it's empty. Verify with a table test: empty → `lan`, `overlay` → `overlay`.
- [x] 1.2 Teach `Config.Validate` to reject any `address` other than `lan` or `overlay`, naming the server, and fail guest-connecting commands on it at server resolution. Verify with config tests, and confirm that `pmox config get-contexts` still works with a bad value.
- [x] 1.3 Document `address:` in the README's config reference. Verify that the docs link check passes.

## 2. IP picker

- [x] 2.1 Change `vmwait.PickIPv4` to take a preference.
  - `lan` adds `tailscale` and `wt` to the skip filter.
  - `overlay` returns the first usable IPv4 on a `tailscale*` or `wt*` interface first, then falls back to the `lan` heuristic.
  - Verify with `internal/vmwait/ip_test.go` cases: overlay listed first in `lan` mode → LAN IP; `overlay` mode with `tailscale0` → 100.x; `overlay` mode with `wt0` → 100.x; `overlay` mode with no overlay interface → LAN IP; overlay as the only interface in `lan` mode → overlay IP via the fallback.
- [x] 2.2 Add `vmwait.AllIPv4(ifaces)`, which returns every usable IPv4 on every interface. Verify with a unit test covering loopback, link-local, and several interfaces.
- [x] 2.3 Pass the server's preference at every guest-connect caller: `cmd/pmox/ssh.go:400`, `mount.go:413`, `sshconfig.go:122`, `list.go:97`, `yardo_fleet.go:227`, `delete.go:410`. Leave `WaitForIP` (launch, start, the reachability wait) on the LAN address. Verify with an existing seam test per command, or a new one, that a fake agent reporting `tailscale0` gives the overlay IP when `address: overlay` is set; `pmox vm ssh-config` prints it as `HostName`.
- [x] 2.4 Update `openspec/decisions.md` (the IP picker skip list) to match. Verify that the listed prefixes match `skipPrefixes`.

## 3. Cleanup

- [x] 3.1 Build the live-IP set in `cmd/pmox/cleanup.go` (around line 370) from `AllIPv4`. (Covered by `TestAllIPv4` and the existing `TestStaleKnownHostItems`; the collector itself has no harness.) Verify with a cleanup test: a VM reporting LAN and `tailscale0` keeps both pins; a pin for an address nobody reports is still stale; an unresponsive agent marks nothing stale.

## 4. Doctor

- [x] 4.1 When `api.reachable` fails to reach a host that is, or resolves to, an RFC1918 address, give a hint about being off the home LAN and needing a Tailscale or NetBird route (`cmd/pmox/doctor.go:558`). Verify with a doctor test: a private host gives the new hint; a public host gives the old one.
- [x] 4.2 Add the subnet-overlap check: the API is unreachable and a local interface's subnet contains the API host → report the overlap and suggest renumbering. Use a seam for the local interface list. Verify with a doctor test using a fake 192.168.1.57/24 interface.
- [x] 4.3 Add the `net.overlay` warning: `address: overlay` is set and there's no local 100.64.0.0/10 address → warn, without failing. Verify with a doctor test: the exit code stays 0 when nothing else fails.
- [x] 4.4 List the new doctor checks and hints in the README's doctor section. Verify that the README describes each check name.

## 5. Remote access guide

- [x] 5.1 Write `docs/remote-access.md` following the outline in design.md.
  - Sections: prerequisites, topologies A/B, Tailscale and NetBird steps, path checks, pmox config, shell/ssh-config, Docker (SSH context and TLS API, published ports and ufw), MCPJungle (loopback tunnel and overlay with a token, Claude Code and Codex lines, `sudo yardo connect`), troubleshooting.
  - Verify that every pmox command in it exists (`pmox <cmd> --help`), and that the yardo command names match yardo's README.
- [x] 5.2 Link the guide from the README (Proxmox-side setup and "Setting up the VM from inside: yardo") and from `llms.txt`. Verify that the link checker passes.

## 6. Integration check

- [x] 6.1 Run `task test` and `golangci-lint run --timeout=3m`. Both must pass.
- [x] 6.2 Manual check (2026-10-30, with `address: overlay` on p0: `tailscale ping payzoff` goes direct over the LAN; `pmox list` shows payzoff at its 100.x address with `(ts)`; `vm ssh-config` writes `HostName 100.104.54.92`; `pmox exec` connects over Tailscale; doctor passes `net.overlay`; `pmox list` from a phone hotspot works through the router) with Tailscale on the PVE node and one VM (`pmox apply web1 -t tailscale`), with `address: overlay` set:
  - At home, `tailscale ping web1` shows a direct LAN path.
  - From a phone hotspot, `pmox shell web1` and `docker --context web1 ps` work, and the MCP gateway answers.
  - Move from the hotspot back to home Wi-Fi with `pmox shell` and `pmox mount` running: the shell survives and the mount keeps syncing.
  - Record the result in the PR.

## Workflow follow-up

- Archive the change once it's merged (`/opsx:archive`).
