# pmox roadmap

pmox is a single static Go binary — a multipass-style CLI for launching and
managing ephemeral VMs on a remote Proxmox VE cluster via the PVE HTTP API.
It does not run on the Proxmox host.

Project shape, build pipeline, and conventions mirror
[tackhq/tack](https://github.com/tackhq/tack). Work is tracked as OpenSpec
slices under `openspec/changes/`.

## Status

| #  | Slice                         | State       | Notes |
|----|-------------------------------|-------------|-------|
| 1  | `project-skeleton`            | ✅ Shipped  | `cmd/pmox`, exit codes, Makefile, goreleaser, CI, release workflow, license, placeholder README |
| 2  | `configure-and-credstore`     | ✅ Shipped  | `pmox configure` with interactive prompts, auto-discovery, keychain, TLS fallback, `--list`, `--remove` |
| 3  | `server-resolution`           | ✅ Shipped  | `internal/server.Resolve` + `--server` root flag + `PMOX_SERVER` |
| 4  | `pveclient-core`              | ✅ Shipped  | Launch/lifecycle endpoints, `WaitTask`, form-body helper, no-retry client |
| 5  | `launch-default`              | ✅ Shipped  | Happy-path `pmox launch` with built-in cloud-init (`ef5e375`) |
| 6  | `list-info-lifecycle`         | ✅ Shipped  | `list`, `info`, `start`, `stop`, `delete`, `clone` (`fef837d`) |
| 10 | `create-template`             | ✅ Shipped  | `pmox create-template` builds an Ubuntu cloud-image template in the 9000–9099 range (`c32ade3`, PVE 9 fix `78ee16e`) |
| 7  | `cloud-init-custom`           | 📋 Planned  | `--cloud-init <file>` full-replace semantics; proposal at `openspec/changes/cloud-init-custom/` |
| 8  | `post-create-hooks`           | 📋 Planned  | `--post-create`, `--tack`, `--ansible`, `--strict-hooks`; proposal at `openspec/changes/post-create-hooks/` |
| 9  | `docs-and-llms-txt`           | 📋 Planned  | Real README, `llms.txt`, `examples/`; proposal at `openspec/changes/docs-and-llms-txt/` |

### Shipped outside the original roadmap

Scope that was originally parked under "Out of scope for v1" but landed as
part of real user flows:

| Slice                              | Notes |
|------------------------------------|-------|
| `ssh-shell-exec`                   | `pmox shell`, `pmox exec` — SSH into / run commands on VMs |
| `cp-sync-commands`                 | `pmox cp`, `pmox sync` — file transfer to/from VMs |
| `mount-unmount`                    | `pmox mount`, `pmox umount` — rsync+fsnotify continuous sync |
| `scp-snippet-upload`               | Move snippet upload from PVE HTTP API to SSH/SFTP via `internal/pvessh` |
| `interactive-target-picker`        | Optional VM argument + picker for single-target commands |
| `confirm-destructive-commands`     | y/N prompt before `pmox delete` |
| `enforce-full-clone`               | Tighten `Clone` spec / behavior to always pass `full=1` |
| `mount-daemon-logs`                | Daemonized mount writes log file, proper pid display |
| `mount-umount-optional-target`     | Mount/umount accept a bare remote path and route through the picker |
| `mount-daemon-default`             | Background mode is now the default for `pmox mount`; `--foreground`/`-F` opts out |
| `ssh-user-precedence`              | Honor `server.user` from config in shell/exec/cp/sync/mount |

Archived slice artifacts live in `openspec/changes/archive/`; the synced
capability specs live in `openspec/specs/`.

## Next up

### 7. `cloud-init-custom`

`--cloud-init <path>` with full-replace semantics on `pmox launch` and
`pmox clone`. Adds a launch-time snippet-storage validator and snippet
cleanup on `pmox delete`. Proposal and tasks live at
`openspec/changes/cloud-init-custom/`.

### 8. `post-create-hooks`

Post-SSH-ready hooks: `--post-create <script>`, `--tack`, `--ansible`,
and `--strict-hooks` to upgrade hook failure from warning to error.
Adds `ExitHook` to `internal/exitcode`. Proposal and tasks live at
`openspec/changes/post-create-hooks/`.

### 9. `docs-and-llms-txt`

Pure-documentation slice: replace the placeholder README with a full
user guide, ship `llms.txt`, and populate `examples/`. Should ship
last so it can document 7 and 8 accurately.

## Out of scope for v1

- LXC containers
- Snapshots
- Multi-VM launch (`--count`)
- Networking beyond DHCP on the default bridge
- Windows builds

## Parked threads

Questions flagged during exploration that don't block progress:

- `--strict-hooks` exit code semantics (to be resolved as part of slice 8)
- Interrupt behavior of `pmox delete` between stop and destroy
- Keychain account-key collision when two pmox installs on one host
  configure the same URL with different credentials
- `openspec/specs/` directory uses `## ADDED Requirements` / `## REMOVED
  Requirements` format but `openspec validate --specs` expects canonical
  `## Purpose` / `## Requirements` sections, so 12 of 15 existing specs
  fail validation. Cosmetic; artifacts in `openspec/changes/archive/` are
  the source of truth. Worth a dedicated cleanup pass.

## Prereqs for the first real release

- Set `HOMEBREW_TAP_TOKEN` in GitHub repo Settings → Secrets and Variables →
  Actions, scoped to write to `eugenetaranov/homebrew-tap`. Without it, the
  goreleaser brew step fails but the GitHub release still publishes.
- Slice 1 task 10.8 (end-to-end release-workflow dry-run via a throwaway tag)
  is deferred to `v0.1.0` itself; `make release-dry-run` has already
  validated the goreleaser config locally.
