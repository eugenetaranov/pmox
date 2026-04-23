# pmox

> pmox is a single static Go binary that launches and manages
> ephemeral VMs on a remote Proxmox VE cluster via the PVE HTTP
> API. It's a multipass-style CLI for homelabs and dev clusters
> where Terraform is too much and the web UI is too slow.

pmox does not run on the Proxmox host. It runs on your laptop,
talks to PVE over HTTPS for VM lifecycle, and over SSH/SFTP for
cloud-init snippet upload. One command builds a template
(`pmox create-template`); another launches a cloud-init-ready VM
and waits for it to be reachable (`pmox launch`). The rest of the
command set — `shell`, `exec`, `cp`, `sync`, `mount`, `umount`,
`list`, `info`, `start`, `stop`, `delete`, `clone` — exists so you
rarely need to open the PVE web UI again.

## Install

Homebrew tap:

```
brew install eugenetaranov/tap/pmox
```

`go install`:

```
go install github.com/eugenetaranov/pmox/cmd/pmox@latest
```

Or download a pre-built binary from the
[releases page](https://github.com/eugenetaranov/pmox/releases).

## Proxmox-side setup

pmox needs three things on the cluster:

1. An API token (`Datacenter → Permissions → API Tokens`)
2. An SSH login to the PVE node (used for cloud-init snippet upload)
3. A cloud-init-ready template with `qemu-guest-agent` installed and
   `agent: 1` set

`pmox create-template` sets up item 3 for you. See
[docs/pve-setup.md](./docs/pve-setup.md) for the full walkthrough
including required privileges, SSH mode tradeoffs, and the most
common first-launch errors.

## Quick start

```
pmox configure                    # walks through API + SSH + defaults
pmox create-template              # optional: bake an Ubuntu template
pmox launch web1                  # clone, cloud-init, wait for SSH
pmox shell web1                   # interactive SSH session
pmox delete web1                  # stop + destroy + snippet cleanup
```

`pmox configure` walks through everything: API URL, token, node SSH
credentials, default node/template/storage/bridge, and your SSH
public key. It writes a starter cloud-init file to
`~/.config/pmox/cloud-init/<slug>.yaml` that you can edit in place.

## Commands

| Command | Summary | Example |
| --- | --- | --- |
| `configure` | Interactive setup: API token, node SSH, defaults, cloud-init starter | `pmox configure` |
| `create-template` | Build an Ubuntu cloud-image template in the 9000–9099 range | `pmox create-template` |
| `launch` | Clone the configured template, push cloud-init, wait for SSH | `pmox launch web1` |
| `clone` | Clone any existing VM or template into a new VM | `pmox clone web1 web2` |
| `list` | List pmox-tagged VMs with IPs; `--all` for every VM | `pmox list` |
| `info` | Show CPU/mem/disk/status/uptime/interfaces for one VM | `pmox info web1` |
| `start` | Start a VM and wait for the guest agent to report an IP | `pmox start web1` |
| `stop` | ACPI graceful shutdown (`--force` for hard stop) | `pmox stop web1` |
| `delete` | Stop + destroy with y/N confirmation (`--yes` to skip) | `pmox delete web1` |
| `shell` | Interactive SSH session; auto-starts a stopped VM | `pmox shell web1` |
| `exec` | Run one command on a VM over SSH | `pmox exec web1 -- uname -a` |
| `cp` | scp-based file copy to or from a VM | `pmox cp ./app.tar web1:/tmp/` |
| `sync` | rsync-based sync to or from a VM | `pmox sync ./src/ web1:/opt/app/` |
| `mount` | Watch a local dir and continuously rsync it to a VM | `pmox mount ./src web1:/opt/app` |
| `umount` | Stop background-mode mounts for a VM | `pmox umount web1` |

Single-target commands (`info`, `start`, `stop`, `delete`, `shell`,
`exec`) accept an optional `[name|vmid]` argument. Omit it and pmox
auto-selects the only pmox-tagged VM when exactly one exists, or
shows an interactive picker when several do. `mount` and `umount`
follow the same auto-select rule when the VM prefix is omitted from
the `[<name|vmid>:]<remote_path>` argument.

Run `pmox <command> --help` for the full flag set of any command.

## Cloud-init

pmox uploads a cloud-init file as a Proxmox `snippets` volume on
every `pmox launch` / `pmox clone` and points the new VM's
`cicustom` at it. The file on disk is the single source of truth
for what ships to the VM — there is no built-in cloud-init mode.

Per-server file:
`~/.config/pmox/cloud-init/<host>-<port>.yaml`

A minimal working example (`pmox configure` writes this for you on
first run, with your selected user and public key substituted in):

```yaml
#cloud-config
users:
  - name: pmox
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - ssh-ed25519 AAAA... your-key-here

package_update: true
packages:
  - qemu-guest-agent

runcmd:
  - systemctl enable --now qemu-guest-agent
```

Edit this file to customize packages, users, `runcmd`, `write_files`,
network config — anything cloud-init supports. Changes apply to the
next launch; running VMs are not updated.

See [examples/cloud-init.yaml](./examples/cloud-init.yaml) for a
copy-and-edit reference.

**Snippet storage vs disk storage.** The storage that holds the
cloud-init snippet and the storage that holds the VM disk are
resolved independently. `pmox configure` picks (or offers to enable
`snippets` content on) a snippet-capable pool and persists it as
`snippet_storage:` in `config.yaml`; the VM disk still lands on
`storage:`. Override per invocation with `--snippet-storage` and
`--storage`. `pmox delete` reads the snippet storage back out of the
VM's `cicustom` value, so cleanup always targets the right pool.

**Rotating the SSH key.** Edit `ssh_pubkey:` in `config.yaml` (or
re-run `pmox configure`), then run `pmox configure --regen-cloud-init`
to rewrite the cloud-init file with the new key.

## Post-create hooks

Once `pmox launch` has an IP and SSH is reachable, you can hand off
to a provisioning tool. Exactly one of `--post-create`, `--tack`,
`--ansible` may be passed per invocation.

```
pmox launch --post-create ./examples/post-create.sh web1
pmox launch --tack ./examples/tack.yaml web1
pmox launch --ansible ./examples/ansible/playbook.yaml web1
```

- `--post-create <script>` runs the script directly (no shell
  wrapper). The environment contains `PMOX_IP`, `PMOX_VMID`,
  `PMOX_NAME`, `PMOX_USER`, `PMOX_NODE`.
- `--tack <config>` runs `tack apply --host <ip> --user <user> <config>`.
  Requires `tack` on PATH — install from
  [tackhq/tack](https://github.com/tackhq/tack).
- `--ansible <playbook>` runs `ansible-playbook` with an inline
  single-host inventory (`-i <ip>,`), the configured SSH user, and
  the derived private key. Requires `ansible-playbook` on PATH.

By default, hook failure prints a warning to stderr and pmox exits
0, leaving the VM reachable for manual follow-up. Pass
`--strict-hooks` to upgrade hook failure to exit code 8 (`ExitHook`).

Hooks are skipped entirely when `--no-wait-ssh` is set — pmox will
not run a command against a VM it has not verified is reachable.

Runnable examples of all three hook shapes live in
[examples/README.md](./examples/README.md).

## Configuration

`pmox configure` writes to `$XDG_CONFIG_HOME/pmox/config.yaml`,
falling back to `~/.config/pmox/config.yaml`. The file is YAML and
holds one block per configured server:

```yaml
servers:
  https://pve.lan:8006:
    token_id: pmox@pve!pmox
    node: pve
    template: ubuntu-24.04
    storage: local-lvm
    snippet_storage: local
    bridge: vmbr0
    ssh_pubkey: /home/you/.ssh/id_ed25519.pub
    user: pmox
    insecure: false
    node_ssh:
      user: root
      auth: key
      key_path: /home/you/.ssh/id_ed25519
mount_excludes:
  - .git
  - node_modules
```

API token secrets, node SSH passwords, and key passphrases are
stored in the system keychain via [go-keyring](https://github.com/zalando/go-keyring),
not in `config.yaml`.

Additional useful invocations:

```
pmox configure --list                # print configured server URLs
pmox configure --remove <url>        # forget a server + its secrets
pmox configure --regen-cloud-init    # rewrite the per-server cloud-init
```

## Environment variables

| Variable | Effect |
| --- | --- |
| `PMOX_SERVER` | Select which configured server to use (overrides default; overridden by `--server`) |
| `PMOX_SSH_INSECURE` | Skip SSH host-key verification; equivalent to `--ssh-insecure` |
| `PMOX_ASSUME_YES` | Skip the `pmox delete` confirmation; equivalent to `--yes` |

Hook scripts receive `PMOX_IP`, `PMOX_VMID`, `PMOX_NAME`, `PMOX_USER`,
`PMOX_NODE` from the launcher — see the post-create hooks section.

## Exit codes

pmox maps typed errors to a small, stable set of exit codes:

| Code | Name | Meaning |
| --- | --- | --- |
| 0 | `ExitOK` | success |
| 1 | `ExitGeneric` | uncategorized error |
| 2 | `ExitUserError` | invalid user input (bad flag value, prompt refusal) |
| 3 | `ExitNotFound` | configured resource missing (server, credential, VM) |
| 4 | `ExitAPIError` | PVE API returned a non-2xx other than 401/404 |
| 5 | `ExitNetworkError` | network or TLS failure reaching the PVE API |
| 6 | `ExitUnauthorized` | 401 from the PVE API (bad token or privilege) |
| 7 | `ExitTimeout` | deadline exceeded (wait-IP, wait-SSH, task polling) |
| 8 | `ExitHook` | `--strict-hooks` and the hook failed |

Scripts that wrap pmox can branch on these reliably; see
`internal/exitcode/exitcode.go` for the canonical definitions.

## Troubleshooting

### `pmox configure` says "no VMs visible on node …"

The token is missing `VM.Audit` on `/vms`. Grant it via the role or
disable privilege separation on the token. See
[docs/pve-setup.md](./docs/pve-setup.md#2-role-and-privileges).

### `pmox launch` times out waiting for an IP

The template was built without `qemu-guest-agent`, or the VM has
`agent: 0`. Rebuild the template with `pmox create-template` or fix
the template by hand per
[docs/pve-setup.md](./docs/pve-setup.md#4-template-preparation).

### `storage does not have 'snippets' in its content types`

Pass `--snippet-storage <pool>` to target a snippet-capable pool, or
enable snippets on the current pool (`pvesm set <pool> --content
images,iso,vztmpl,rootdir,snippets`). `pmox configure` can do the
latter if you grant it `Datastore.Allocate`.

### `pmox delete` exits with "stdin is not a TTY"

Scripts need `--yes` (or `PMOX_ASSUME_YES=1`) to bypass the
confirmation prompt. `--force` is orthogonal — it bypasses the tag
check, not the prompt.

### Snippet upload fails with an SSH handshake error

The pinned host key in `~/.config/pmox/known_hosts` no longer matches
the PVE node (common after a reinstall). Delete the stale line and
rerun; pmox will re-pin on the next connection. pmox never touches
`~/.ssh/known_hosts`.

### `pmox mount` stops silently in the background

Background mounts write a PID file and log file under
`~/.config/pmox/mount/`. `pmox umount <vm>` stops every mount for a
VM; inspect the log file to find out why a mount exited.

### Hook exited non-zero but `pmox launch` exited 0

That is the default — hook failure prints to stderr and returns
success, so the VM stays reachable for manual follow-up. Pass
`--strict-hooks` to upgrade hook failure to exit code 8.

## Development

```
make build             # ./bin/pmox
make test              # unit tests
make lint              # golangci-lint
make docs-check        # validate README/llms.txt/docs/examples links
make release-dry-run   # local goreleaser snapshot
```

The project layout mirrors [tackhq/tack](https://github.com/tackhq/tack):
commands under `cmd/pmox/`, logic under `internal/`, slices tracked
as OpenSpec proposals under `openspec/changes/`, shipped specs under
`openspec/specs/`, and the roadmap in
[ROADMAP.md](./ROADMAP.md).

## License

[MIT](./LICENSE).
