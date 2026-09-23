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
pmox init                    # walks through API + SSH + defaults
pmox create-template              # optional: bake an Ubuntu template
pmox launch web1                  # clone, cloud-init, wait for SSH
pmox shell web1                   # interactive SSH session
pmox delete web1                  # stop + destroy + snippet cleanup
```

`pmox init` walks through everything: API URL, token, node SSH
credentials, default node/template/storage/bridge, and your SSH
public key. It writes a starter cloud-init file to
`~/.config/pmox/cloud-init/<slug>.yaml` that you can edit in place.

The URL prompt is forgiving — type a bare IP (`10.0.0.5`), a hostname
(`pve.lan`), `host:port`, or paste the web-UI address; the scheme
(`https`) and port (`8006`) are filled in for you. configure probes the
endpoint **before** asking for a token: if nothing is listening it shows
the error and re-asks the address in place (blank line or Ctrl-C to
quit). Discovery steps with a single option (node/template/storage/
bridge) are chosen automatically. At the SSH-key step you can **generate
a new dedicated bootstrap key**, pick an existing one, or browse the
filesystem for it.

For the API token you can either paste one you created in the web UI, or
let pmox **generate it for you**: choose "generate", enter a
`user@realm` (e.g. `root@pam`) and password, and pmox logs in, creates
the token (with privilege separation off, so it inherits your user's
rights), and stores only the token secret. Your **password is used once
to log in and is never saved** — only the generated token secret goes to
the keyring (or the file fallback).

## Commands

`pmox --help` groups these into the same three sections shown below.
Every command is invoked flat — e.g. `pmox launch web1` — grouping is
just for readability.

### VM lifecycle

| Command | Summary | Example |
| --- | --- | --- |
| `launch` | Clone the configured template, push cloud-init, wait for SSH | `pmox launch web1` |
| `clone` | Clone an existing VM/template into a new VM (source optional → picker) | `pmox clone web1 web2` |
| `start` | Start a VM and wait for the guest agent to report an IP | `pmox start web1` |
| `stop` | ACPI graceful shutdown of one or more VMs (`--force` for hard stop) | `pmox stop web1 web2` |
| `delete` | Stop + destroy one or more VMs with y/N confirmation (`--yes` to skip) | `pmox delete web1 web2` |
| `list` | List pmox-tagged VMs with IPs; `--all` for every VM | `pmox list` |
| `info` | Show CPU/mem/disk/status/uptime/interfaces for one VM | `pmox info web1` |

### Access & files

| Command | Summary | Example |
| --- | --- | --- |
| `shell` | Interactive SSH session; auto-starts a stopped VM | `pmox shell web1` |
| `exec` | Run one command on a VM over SSH | `pmox exec web1 -- uname -a` |
| `apply` | Run a tack playbook against a VM (reuses pmox's SSH) | `pmox apply web1` |
| `cp` | scp-based file copy to or from a VM | `pmox cp ./app.tar web1:/tmp/` |
| `sync` | rsync-based sync to or from a VM | `pmox sync ./src/ web1:/opt/app/` |
| `mount` | Watch a local dir and continuously rsync it to a VM | `pmox mount ./src web1:/opt/app` |
| `umount` | Stop background-mode mounts for a VM | `pmox umount web1` |
| `ssh-config` | Print SSH connection details (config block or `--command`) | `pmox ssh-config web1` |

### Setup & diagnostics

| Command | Summary | Example |
| --- | --- | --- |
| `init` | Interactive setup: API token, node SSH, defaults, cloud-init starter | `pmox init` |
| `config` | Manage contexts (servers) kubectl-style (`get-contexts`/`use-context`/`current-context`/`rename-context`/`delete-context`) | `pmox config use-context prod` |
| `create-template` | Build an Ubuntu cloud-image template in the 9000–9099 range | `pmox create-template` |
| `doctor` | Validate config + Proxmox connectivity; report if pmox is ready | `pmox doctor` |
| `cleanup` | Report/remove pmox leftovers: orphaned snippets + stale local state | `pmox cleanup --apply` |

### Interactive selection

Most target-taking commands work without typing a VM name. Omit it and
pmox auto-selects the only pmox-tagged VM when exactly one exists, or
shows an **arrow-key picker** when several do:

- `info`, `start`, `stop`, `delete`, `shell`, `exec`, `ssh-config` — omit the `[name|vmid]`.
- `stop` and `delete` show a **multi-select** picker (space to toggle, enter to confirm) and also accept several names at once.
- `cp` / `sync` — use a bare `:` (e.g. `pmox cp ./app.tar :/tmp/`) to pick the VM.
- `clone <new-name>` — omit the source to pick it.
- `mount` / `umount` — omit the VM prefix of `[<name|vmid>:]<remote_path>`.
- `config use-context` — omit the name to pick a context.

Pickers are strictly non-obtrusive: an explicit arg always skips them,
they only appear on a terminal, and they never draw in scripts, pipes,
or `--output json`. Force non-interactive behavior anywhere with
`--no-input` (or `PMOX_NO_INPUT=1`) — pmox then errors with the exact
argument to pass instead of prompting.

Run `pmox <command> --help` for the full flag set of any command.

## Cleaning up leftovers

`pmox cleanup` reclaims cruft pmox can leave behind and is **dry-run by
default** — it reports what it would remove; pass `--apply` to delete. It
scans every configured context, in selectable categories:

- **snippet** — orphaned cloud-init snippets on the cluster (`pmox-<vmid>-…`) whose VM no longer exists;
- **mount-record** / **log** — dead mount records and orphaned mount logs in the local state dir;
- **cloud-init** — `~/.config/pmox/cloud-init/<slug>.yaml` files for servers removed from your config;
- **tack-profile** — remembered tack profiles for servers/VMs that no longer exist;
- **secret** — file-backend `secrets.yaml` entries for removed servers (OS-keychain secrets can't be enumerated, so they're cleared at removal time by `configure --remove` instead);
- **known-host** — stale guest `known_hosts` pins (skipped entirely if pmox can't enumerate every running VM's IP, so a valid pin is never dropped);
- **template** — pmox-generated templates (`ubuntu-…-pmox-…`, 9000–9099). **Destructive: this deletes VMs.** It is never selected by default — tick it in the checklist or pass `--include-templates`.

On a terminal, `pmox cleanup` shows a **checklist** to pick categories
(non-destructive ones pre-checked, `template` unchecked). Non-interactively,
scope with `--only`/`--skip`:

```
pmox cleanup                          # checklist (or safe categories) — report only
pmox cleanup --apply                  # remove the selected items
pmox cleanup --only snippet,log       # just these
pmox cleanup --skip known-host        # everything safe except this
pmox cleanup --include-templates --apply   # also delete pmox templates
pmox cleanup --output json
```

Aside from opted-in `template` removal, VMs are never touched — use
`pmox delete`.

## Checking readiness

`pmox doctor` runs read-only checks and tells you whether pmox is ready
to launch VMs — validating config, API reachability and token
privileges, the target node's online status, storage/template
readiness, node SSH, and local tooling. It never prompts or changes
anything.

```
pmox doctor            # human report; exits non-zero if any check fails
pmox doctor --strict   # also fail on warnings (good for CI)
pmox doctor --output json | jq '.checks[] | select(.status=="fail")'
```

Each check reports `✓ pass`, `! warn`, or `✗ fail` with an exact
remediation hint (e.g. the missing privilege name and the `pveum` line
to grant it, or the `pvesm set … snippets` command). Warnings don't
block readiness unless `--strict` is set. The exit code follows the
same taxonomy as other commands (unauthorized, network, not-found,
timeout, …), so CI can gate on it; `--output json` (with a stable
`schema_version` and per-check `id`) is the machine-readable form.
Add `--verbose` to also list passing checks.

## Cloud-init

pmox uploads a cloud-init file as a Proxmox `snippets` volume on
every `pmox launch` / `pmox clone` and points the new VM's
`cicustom` at it. The file on disk is the single source of truth
for what ships to the VM — there is no built-in cloud-init mode.

Per-server file:
`~/.config/pmox/cloud-init/<host>-<port>.yaml`

A minimal working example (`pmox init` writes this for you on
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
resolved independently. `pmox init` picks (or offers to enable
`snippets` content on) a snippet-capable pool and persists it as
`snippet_storage:` in `config.yaml`; the VM disk still lands on
`storage:`. Override per invocation with `--snippet-storage` and
`--storage`. `pmox delete` reads the snippet storage back out of the
VM's `cicustom` value, so cleanup always targets the right pool.

**Rotating the SSH key.** Edit `ssh_pubkey:` in `config.yaml` (or
re-run `pmox init`), then run `pmox init --regen-cloud-init`
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
- `--tack <playbook>` runs `tack run <playbook> -c ssh://<user>@<ip>
  --ssh-key <identity>` against the new VM (auto-approved). Omit the
  value (`--tack`) to use `~/.config/pmox/tack/playbook.yaml`. Requires
  `tack` on PATH — install from
  [tackhq/tack](https://github.com/tackhq/tack). See **Provisioning with
  tack** below for the day-2 `pmox apply` command.
- `--ansible <playbook>` runs `ansible-playbook` with an inline
  single-host inventory (`-i <ip>,`), the configured SSH user, and
  the derived private key. Requires `ansible-playbook` on PATH.

By default, hook failure prints a warning to stderr and pmox exits
0, leaving the VM reachable for manual follow-up. Pass
`--strict-hooks` to upgrade hook failure to exit code 8 (`ExitHook`).

Hooks are skipped entirely when `--no-wait-ssh` is set — pmox will
not run a command against a VM it has not verified is reachable.

## Provisioning with tack

`pmox apply [vm]` runs a [tack](https://github.com/tackhq/tack) playbook
against an existing VM (day-2 convergence), reusing the SSH user and key
pmox already knows. The VM is auto-started if stopped.

Playbooks and roles live under `~/.config/pmox/tack/`:

```
~/.config/pmox/tack/
  playbook.yaml     # default, used by `pmox apply <vm>`
  web.yaml          # a named profile: `pmox apply <vm> web`
  roles/            # local roles (or reference tack-roles remotely)
```

Run `pmox apply --init` to scaffold that layout with a starter playbook.
The playbook is resolved in order: `--playbook <path>` → a profile
argument (`<profile>.yaml`) → the profile last used for that VM
(remembered per VM) → `playbook.yaml`.

```
pmox apply web1                 # default or remembered profile
pmox apply web1 web             # ~/.config/pmox/tack/web.yaml (remembered)
pmox apply web1 --check         # plan only (tack --check)
pmox apply web1 -t docker       # only tasks tagged 'docker'
pmox apply web1 --playbook ./p.yaml
```

tack's own plan/apply confirmation is shown; pass `-y` (or
`PMOX_ASSUME_YES=1`) to auto-approve. **Host keys:** tack verifies against
`~/.ssh/known_hosts` (independently of pmox's own `known_hosts_guests`),
so the first apply to a brand-new VM may report an unknown host key —
scan it (`ssh-keyscan -H <ip> >> ~/.ssh/known_hosts`) or pass
`--ssh-insecure`. `pmox doctor` reports whether tack and a default
playbook are present.

Runnable examples of all three hook shapes live in
[examples/README.md](./examples/README.md).

## Configuration

`pmox init` writes to `$XDG_CONFIG_HOME/pmox/config.yaml`,
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
never in `config.yaml`.

**Headless / no keychain.** When no OS keychain is available (headless
Linux without a Secret Service, CI, containers), pmox automatically falls
back to a `0600` file at `~/.config/pmox/secrets.yaml` so it still works —
secrets are still kept out of `config.yaml`. Force a backend with
`PMOX_SECRET_STORE=keychain|file` (default `auto`); `keychain` errors if
none is present, `file` always uses the file. Under `auto`, reads try the
keychain then the file, so secrets follow you if the environment changes.
The file fallback is plaintext protected only by file permissions —
`pmox doctor` warns when it's in use.

Additional useful invocations:

```
pmox init --regen-cloud-init    # rewrite the per-server cloud-init
```

`--regen-cloud-init` also lets you **pick a different SSH key** (it
defaults to the current one); a changed key is saved to config and
written into the cloud-init template. Re-running plain `pmox init`
and selecting a new key will likewise offer to regenerate the cloud-init
when it detects the file authorizes a different key. Either way, relaunch
existing VMs for a new key to take effect (cloud-init only runs at first
boot). `pmox doctor` flags this drift (`ssh_pubkey` vs the key in the
cloud-init file) before it turns into a `Permission denied (publickey)`.

### Contexts (multiple servers)

Each configured server is a **context** (kubectl-style), addressed by a
short name. When you have more than one, switch between them instead of
passing `--server` every time:

```
pmox config get-contexts             # table of contexts; current marked *
pmox config use-context prod         # switch the current context
pmox config current-context          # print the current context
pmox config rename-context 192.168.0.185 prod
pmox config delete-context lab       # forget a context + its secrets
pmox config path                     # print the config file location
```

A new context's name defaults to its host; `rename-context` gives it a
friendly name. Target a specific context for one command with `--context
<name>` (or `--server <name|url>`).

Each command resolves its target in this order: `--server` (name or URL)
→ `--context` (name) → `PMOX_SERVER` → `PMOX_CONTEXT` → the current
context (`use-context`) → the only configured context → an interactive
picker. (`pmox init --list` / `--remove` still work.)

## Environment variables

| Variable | Effect |
| --- | --- |
| `PMOX_SERVER` | Select the target context by name or URL (overridden by `--server`) |
| `PMOX_CONTEXT` | Select the target context by name (overridden by `--context`) |
| `PMOX_SSH_INSECURE` | Skip SSH host-key verification; equivalent to `--ssh-insecure` |
| `PMOX_ASSUME_YES` | Skip the `pmox delete` confirmation; equivalent to `--yes` |
| `PMOX_NO_INPUT` | Never prompt; error instead of showing a picker; equivalent to `--no-input` |
| `PMOX_SECRET_STORE` | Secret backend: `auto` (default), `keychain`, or `file` (`~/.config/pmox/secrets.yaml`, 0600) |

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

### `pmox init` says "no VMs visible on node …"

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
images,iso,vztmpl,rootdir,snippets`). `pmox init` can do the
latter if you grant it `Datastore.Allocate`.

### `pmox delete` exits with "stdin is not a TTY"

Scripts need `--yes` (or `PMOX_ASSUME_YES=1`) to bypass the
confirmation prompt. `--force` is orthogonal — it bypasses the tag
check, not the prompt.

### `TLS certificate for … CHANGED — possible MITM`

When a server is configured with `insecure: true` (self-signed cert),
pmox warns once — on the first connect — that the transport is
unverified, then pins the certificate's SHA-256 fingerprint in the
config. On later runs the pinned cert is verified silently (it is now
authenticated against the pin, like SSH's known_hosts). If the presented
certificate ever stops matching the pin, pmox refuses to proceed. If you deliberately replaced or renewed the
node's certificate, clear `tls_pin_sha256` for that server in
`~/.config/pmox/config.yaml` (or re-run `pmox init`) and pmox will
re-pin on the next connect.

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
