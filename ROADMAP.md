# pmox roadmap

pmox is a single static Go binary — a multipass-style CLI for launching and
managing ephemeral VMs on a remote Proxmox VE cluster via the PVE HTTP API.
It does not run on the Proxmox host.

Project shape, build pipeline, and conventions mirror
[tackhq/tack](https://github.com/tackhq/tack). Work is tracked as OpenSpec
slices under `openspec/changes/`.

## Status

| # | Slice                    | State     | Notes |
|---|--------------------------|-----------|-------|
| 1 | `project-skeleton`       | ✅ Shipped | `cmd/pmox`, exit codes, Makefile, goreleaser, CI, release workflow, license, placeholder README |
| 2 | `configure-and-credstore`| ✅ Shipped | `pmox configure` with interactive prompts, auto-discovery, keychain, TLS fallback, `--list`, `--remove` |
| 3 | `server-resolution`      | ✅ Shipped | `internal/server.Resolve` + `--server` root flag + `PMOX_SERVER`; unit-tested, first real caller in slice 5 |
| 4 | `pveclient-core`         | 📋 Planned | HTTP client endpoints for launch-time ops |
| 5 | `launch-default`         | 📋 Planned | Happy-path launch with built-in cloud-init |
| 6 | `list-info-lifecycle`    | 📋 Planned | `list`, `info`, `start`, `stop`, `delete`, `clone` |
| 7 | `cloud-init-custom`      | 📋 Planned | `--cloud-init` (full replace only) |
| 8 | `post-create-hooks`      | 📋 Planned | `--post-create`, `--tack`, `--ansible`, `--strict-hooks` |
| 9 | `docs-and-llms-txt`      | 📋 Planned | Real README, llms.txt, examples/ |

Archived slice artifacts live in `openspec/changes/archive/`; the synced
capability specs live in `openspec/specs/`.

## Shipped

### 1. `project-skeleton`

Buildable `pmox --version` binary with the full release pipeline in place.
Cobra root command, persistent flags (`--debug`, `--verbose`, `--no-color`,
`--output`), `internal/exitcode` with typed exit codes, Makefile mirroring
tack (`build`, `test`, `lint`, `release`, `release-dry-run`), goreleaser v2
config for linux+darwin × amd64+arm64, GitHub Actions CI + release workflows,
MIT license, placeholder README.

### 2. `configure-and-credstore`

`pmox configure` subcommand — interactively walks through API URL, token,
credential validation against `/version`, TLS fallback on self-signed certs,
and auto-discovery of node, template, storage, and bridge. The token secret
is stored in the system keychain via `go-keyring`; everything else is written
to `$XDG_CONFIG_HOME/pmox/config.yaml` (mode 0600, parent dir 0700).
Supports `--list` and `--remove <url>`. SSH key picker scans `~/.ssh`
recursively for `.pub` files.

## Shipped (continued)

### 3. `server-resolution`

`internal/server.Resolve(ctx, opts) (*Resolved, error)` implements a
five-rung precedence ladder: `--server` flag → `PMOX_SERVER` env →
single configured server → interactive picker (TTY only) → error. Input
via flag or env is canonicalized (with `https://` auto-prepend) and
exact-matched against the config map — no prefix magic. The returned
`Resolved` bundles canonical URL, `*config.Server`, and the token
secret pulled from `credstore`; a missing keychain entry is a hard
error with a "re-run `pmox configure`" hint. `selectOne` was extracted
from `configure.go` into a shared `internal/tui` package so both
callers reuse it. `--server` is a persistent root flag; `configure`
ignores it. Dead code until slice 5 — shipped now so slice 5 stays
small.

## Next up

### 4. `pveclient-core`

Extends the minimal `internal/pveclient` with launch-time endpoints:
`NextID`, `Clone`, `Resize`, `SetConfig`, `Start`, `AgentNetwork`, `Delete`,
`GetStatus`. Mostly mechanical once the PVE API docs are open.

### 5. `launch-default`

The first slice where all five cross-slice decisions (D-T1..D-T5) cash out.
Happy-path `pmox launch` with built-in cloud-init only. Should not be drafted
until slice 4 is solid.

## Later

- **6. `list-info-lifecycle`** — `list`, `info`, `start`, `stop`, `delete`, `clone`
- **7. `cloud-init-custom`** — `--cloud-init` (full replace, per D-T5)
- **8. `post-create-hooks`** — `--post-create`, `--tack`, `--ansible`, `--strict-hooks`
- **9. `docs-and-llms-txt`** — real README, `llms.txt`, `examples/`

## Out of scope for v1

- LXC containers
- Snapshots
- Multi-VM launch (`--count`)
- `pmox shell` / `pmox exec`
- Host directory mounts
- Networking beyond DHCP on the default bridge
- Windows builds

## Parked threads

Questions flagged during exploration that don't block progress:

- `--strict-hooks` exit code semantics
- Interrupt behavior of `pmox delete` between stop and destroy
- Keychain account-key collision when two pmox installs on one host
  configure the same URL with different credentials
- Whether the picker helper should be abstracted and shared between
  configure's auto-discovery and server-resolution's multi-server prompt

## Prereqs for the first real release

- Set `HOMEBREW_TAP_TOKEN` in GitHub repo Settings → Secrets and Variables →
  Actions, scoped to write to `eugenetaranov/homebrew-tap`. Without it, the
  goreleaser brew step fails but the GitHub release still publishes.
- Slice 1 task 10.8 (end-to-end release-workflow dry-run via a throwaway tag)
  is deferred to `v0.1.0` itself; `make release-dry-run` has already
  validated the goreleaser config locally.
