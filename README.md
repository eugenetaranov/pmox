# pmox

> pmox is a single static Go binary that launches and manages
> ephemeral VMs on a remote Proxmox VE cluster via the PVE HTTP
> API. It's a multipass-style CLI for homelabs and dev clusters
> where Terraform is too much and the web UI is too slow.

pmox does not run on the Proxmox host. It runs on your laptop,
talks to PVE over HTTPS for VM lifecycle, and over SSH/SFTP for
cloud-init snippet upload. One command builds a template
(`pmox template create`); another launches a cloud-init-ready VM
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

To upgrade later, run `pmox version upgrade`. It finds the latest release
and upgrades the same way pmox was installed: through `brew update` and
`brew upgrade` for the Homebrew tap (it says so if the tap's formula hasn't
caught up with the release yet), or by downloading the release, verifying its checksum and
replacing the binary for everything else. It uses sudo, which asks for
your password, when the binary's directory isn't writable. It asks before
upgrading; `--check` only reports, and `--yes` skips the question (needed
without a terminal).

## Proxmox-side setup

pmox needs three things on the cluster:

1. An API token (`Datacenter → Permissions → API Tokens`)
2. An SSH login to the PVE node (used for cloud-init snippet upload)
3. A cloud-init-ready template with `qemu-guest-agent` installed and
   `agent: 1` set

`pmox template create` sets up item 3 for you. See
[docs/pve-setup.md](./docs/pve-setup.md) for the full walkthrough
including required privileges, SSH mode tradeoffs, and the most
common first-launch errors.

Away from home, pmox needs an overlay network (Tailscale or NetBird) to
reach the cluster and its VMs. `pmox router create` builds a small
Tailscale router container on the node's bridge that puts the node's API
and SSH on your tailnet; [docs/remote-access.md](./docs/remote-access.md)
sets one up so it switches by itself when you're back on the LAN, and
covers connecting your laptop's Docker and MCP clients to a VM.

## Quick start

```
pmox init                    # walks through API + SSH + defaults
pmox template create              # optional: bake an Ubuntu template
pmox launch web1                  # clone, cloud-init, wait for SSH (alias: create)
pmox shell web1                   # interactive SSH session
pmox delete web1                  # stop + destroy + snippet cleanup
```

On a terminal, `pmox launch` with no name asks for one plus
`--cpu`/`--mem`/`--disk` (blank keeps the default shown in brackets);
`pmox launch web1` skips the name and only asks for whichever of those
three you didn't pass as a flag. Non-interactively, a missing name is
still an error and unset sizing flags silently use the built-in
default (2 cores / 2 GiB / 20 GiB), same as always.

On a terminal, running `pmox` with no command at all shows an
arrow-key (or type-to-filter) picker of every command, grouped the
same way `pmox --help` is; picking one runs it with no further
arguments, so that command's own prompts (a VM picker, `launch`'s
name/sizing prompts, etc.) take over from there. Non-interactively
this still just prints help, as before.

`pmox init` walks through everything: API URL, token, node SSH
credentials, default node/template/storage/bridge, and your SSH
public key. It writes a starter cloud-init file to
`~/.config/pmox/cloud-init/<slug>.yaml` that you can edit in place.

On a terminal, `pmox init` is a **single-screen wizard**: a tab bar
(Connection › Defaults › Access › Review) stays put while each page is
redrawn in place. Connection takes the URL and token. Defaults shows
node, template, storage, snippet storage and bridge on one page;
changing the node reloads its options. Access holds the SSH key, default
user and node SSH login. The flow ends on a **review screen** that lists
everything and lets you jump back and change any answer. Nothing is
written until you confirm. Probes, logins and SSH checks run with an
inline spinner. A failure is shown on its own page with your answers
kept. Trust questions (overwrite a server, a changed TLS certificate,
the node's SSH host key, enabling snippets) appear as dialogs. **Esc**
goes back a page, **Ctrl-C** quits without saving. Piped, `--no-input`
and CI runs fall back to plain prompts.

Already set up? `pmox init` won't start over. It asks whether to change
a configured server (`pmox config edit`), add another one
(`pmox context add`) or remove one (`pmox context delete`).

`pmox launch` on a terminal with no default template settles one before
asking anything else. It offers the cluster's templates, or builds a new
one right away if there are none (Ctrl-C cancels), and saves the choice
as the default.

The URL prompt is forgiving — type a bare IP (`10.0.0.5`), a hostname
(`pve.lan`), `host:port`, or paste the web-UI address; the scheme
(`https`) and port (`8006`) are filled in for you. configure probes the
endpoint **before** asking for a token: if nothing is listening it shows
the error and re-asks the address in place (blank line or Ctrl-C to
quit). Discovery steps with a single option (node/storage/bridge) are
chosen automatically. The template step is the exception: on a
terminal it always offers **"Build a new Ubuntu template now"**
alongside any existing templates, even when there's only one — picking
it runs the full `pmox template create` build right there (once node
SSH is set up, a few steps later) and sets the result as the default,
instead of always requiring you to have one ready beforehand. At the
SSH-key step you can **generate a new dedicated bootstrap key**, pick
an existing one, or browse the filesystem for it.

For the API token you can either paste one you created in the web UI, or
let pmox **generate it for you**: choose "generate", enter a
`user@realm` (e.g. `root@pam`) and password, and pmox logs in, creates
the token (with privilege separation off, so it inherits your user's
rights), and stores only the token secret. Your **password is used once
to log in and is never saved** — only the generated token secret goes to
the keyring (or the file fallback).

## Commands

Commands are grouped by what they act on: `pmox <noun> <verb>`, e.g.
`pmox vm list` or `pmox template create`. The ones you type every day
are also available directly at the top level. `pmox list` is exactly
`pmox vm list`, with the same flags, output and exit codes. Running a
noun alone (`pmox vm`) on a terminal shows its verbs to pick from.

### Shortcuts (daily commands)

| Command | Same as | Example |
| --- | --- | --- |
| `init` | first-run setup wizard (on a configured machine: edit, add or remove a server) | `pmox init` |
| `launch` | `vm launch` | `pmox launch web1` |
| `shell` | `vm shell` | `pmox shell web1` |
| `list`, `ls` | `vm list` | `pmox ls` |
| `start` / `stop` | `vm start` / `vm stop` | `pmox stop web1 web2` |
| `delete`, `rm` | `vm delete` | `pmox rm web1` |
| `exec` | `vm exec` | `pmox exec web1 -- uname -a` |
| `cp` / `sync` | `vm cp` / `vm sync` | `pmox sync ./src/ web1:/opt/app/` |
| `apply` | `vm apply` | `pmox apply web1` |
| `mount` / `umount` | `mount create` / `mount delete` | `pmox mount ./src web1:/opt/app` |

### `pmox info`: the Proxmox host

`pmox info` shows the current context's Proxmox host: its version, each
node's status, uptime, CPU and memory use, its storage pools with free
space, and how many VMs run there (pmox's and in all). `--context` picks
another server and `-o json` prints it as JSON. For one VM, use `pmox vm info
<name>`.

### `pmox vm`: VMs

| Verb | Summary | Example |
| --- | --- | --- |
| `launch` | Clone the configured template, push cloud-init, wait for SSH | `pmox vm launch web1` |
| `clone` | Clone an existing VM/template into a new VM (source optional → picker) | `pmox vm clone web1 web2` |
| `list`, `ls` | List pmox-tagged VMs with IPs; `--all` for every VM | `pmox vm list` |
| `info` | Show CPU/mem/disk/status/uptime/interfaces for one VM | `pmox vm info web1` |
| `start` | Start a VM and wait for the guest agent to report an IP | `pmox vm start web1` |
| `stop` | ACPI graceful shutdown of one or more VMs (`--force` for hard stop) | `pmox vm stop web1 web2` |
| `resize ram`, `cpu`, `disk` | Set memory (GiB) or cores (applied at the next stop/start; `--restart` does it now), or grow the disk (`+20` or `60` GiB) and, on a running VM, its filesystem | `pmox vm resize disk web1 +20` |
| `delete`, `rm` | Stop + destroy one or more VMs with y/N confirmation (`--yes` to skip) | `pmox vm delete web1` |
| `shell` | Interactive SSH session; auto-starts a stopped VM | `pmox vm shell web1` |
| `exec` | Run one command on a VM over SSH | `pmox vm exec web1 -- uname -a` |
| `cp` | scp-based file copy to or from a VM | `pmox vm cp ./app.tar web1:/tmp/` |
| `sync` | rsync-based sync to or from a VM | `pmox vm sync ./src/ web1:/opt/app/` |
| `apply` | Run a tack playbook against a VM (reuses pmox's SSH) | `pmox vm apply web1` |
| `ssh-config` | Print SSH connection details (config block or `--command`) | `pmox vm ssh-config web1` |

### Other resources

| Command | Summary | Example |
| --- | --- | --- |
| `template create` / `list` | Build an Ubuntu cloud-image template (9000–9099) / list templates | `pmox template create` |
| `context list` / `add` / `use` / `current` / `rename` / `delete` | Add and switch between Proxmox servers | `pmox context add` |
| `config edit` / `path` / `cloud-init` | Edit a context; config file path; show or `--regenerate` the cloud-init template | `pmox config edit prod` |
| `mount create` / `list` / `delete` | Continuous one-way rsync of a local dir to a VM, `.git` and gitignored files included (`--gitignore` leaves those out); changes on the VM are overwritten, so commit locally; list or stop background mounts | `pmox mount list` |
| `key list` / `publish` / `unpublish` / `show` | Published keys on the cluster; publish or remove yours | `pmox key list` |
| `access setup` / `grant` / `revoke` / `show` / `sync` | Share VMs with other people (`setup` is interactive) | `pmox access grant web1 --to bob` |
| `router create` / `login` / `status` / `route add`/`rm`/`list` / `delete` | A Tailscale router container that puts the node on your tailnet ([docs/remote-access.md](./docs/remote-access.md)) | `pmox router create --auth-key-file ~/ts.key` |
| `yardo push` / `apply` / `verify` / `gitkey` | Install yardo on a VM; apply profiles across VMs (4 at a time); check them for drift; send an SSH key for git | `pmox yardo gitkey web1 ~/.ssh/deploy_key` |

### Maintenance

| Command | Summary | Example |
| --- | --- | --- |
| `doctor` | Validate config + Proxmox connectivity; report if pmox is ready | `pmox doctor` |
| `cleanup` | Report/remove pmox leftovers: orphaned snippets + stale local state | `pmox cleanup --apply` |

### Renamed commands

These old forms still work, but print a one-line note on stderr. Their
output and exit codes are unchanged. They will be removed after at
least two more minor releases:

| Old | New |
| --- | --- |
| `pmox create-template` | `pmox template create` |
| `pmox config get-contexts` / `use-context` / `current-context` / `rename-context` / `delete-context` | `pmox context list` / `use` / `current` / `rename` / `delete` |
| `pmox init --list` / `--remove <url>` | `pmox context list` / `delete <name>` |
| `pmox init --regen-cloud-init` | `pmox config cloud-init --regenerate` |
| `pmox ssh-config`, `pmox clone` | `pmox vm ssh-config`, `pmox vm clone` |

### Interactive selection

Most target-taking commands work without typing a VM name. Omit it and
pmox auto-selects the only pmox-tagged VM when exactly one exists, or
shows an **arrow-key picker** when several do:

- `info`, `start`, `stop`, `delete`, `shell`, `exec`, `ssh-config`, `apply` — omit the `[name|vmid]`.
- `stop` and `delete` show a **multi-select** picker (space to toggle, enter to confirm) and also accept several names at once.
- `cp` / `sync` — use a bare `:` (e.g. `pmox cp ./app.tar :/tmp/`) to pick the VM, or omit both arguments entirely and pmox asks for a direction (upload/download), then the local path and the `<vm>:<path>` target (see below).
- `clone <new-name>` — omit the source to pick it, or omit both source and new name and pmox asks for each in turn.
- `exec [name|vmid]` — omit the remote command after `--` and pmox asks for it.
- `mount` — omit the VM prefix of `[<name|vmid>:]<remote_path>`, give only the local directory (`pmox mount ./src`), or omit both and pmox asks for the local directory and the target.
- `umount` — omit the argument and pmox lists the running mounts to stop (one running mount is stopped without asking).
- `context use` — omit the name to pick a context.

**Agent and port forwarding.** `pmox shell -A web1` forwards your local
ssh-agent, so git on the VM uses your laptop's keys and no key has to live
on the VM; `pmox exec -A` does the same for one command. `pmox shell -L 3000
web1` (repeatable) forwards a local port to the VM (`3000:localhost:3000`;
any `ssh -L` spec is passed through), e.g. for a dev server or an OIDC login
callback. `forward_agent: true` under a server turns `-A` on for its VMs
(`--forward-agent=false` overrides it once), and `pmox vm ssh-config` then
writes `ForwardAgent yes`. While you're connected, root on the VM can use
the forwarded agent.

**The `<vm>:<path>` target field** (mount, cp, sync) lists your pmox
VMs under the input and suggests a greyed default — `web1:/mnt/<local
folder>` for `mount`, `web1:~/<local folder>` for uploads:

- **Tab** completes the VM name (extending to the common prefix, then
  cycling), then remote directories (one SSH listing per directory);
  **→**/**End** accept the greyed suggestion.
- **↑/↓** picks another VM and keeps the path you typed.
- Everything stays editable: backspace into the path or the VM name,
  retype, or pick again. **Enter** confirms; **Esc** goes back to the
  previous field.
- With exactly one pmox VM, the field opens as `web1:` with the cursor
  in the path — you're never asked for the VM.
- A remote path that starts with neither `/` nor `~/` is relative to
  the login user's home: `project/pmox` is `~/project/pmox`.

The local path field defaults to `.` and Tab-completes local paths.

**A missing destination directory** is confirmed before anything is
copied: `Create ~/project/pmox on web1?` (default Yes) creates it, owned
by the login user — with `sudo` when the parent isn't writable, as for
`/mnt`. Scripts pass `--mkdir` to create it without asking; without a
terminal and without `--mkdir`, pmox stops with an error naming the
path.

Pickers are strictly non-obtrusive: an explicit arg always skips them,
they only appear on a terminal, and they never draw in scripts, pipes,
or `--output json`. Force non-interactive behavior anywhere with
`--no-input` (or `PMOX_NO_INPUT=1`) — pmox then errors with the exact
argument to pass instead of prompting.

Run `pmox <command> --help` for the full flag set of any command.

## Sharing VMs between users

Each VM authorizes the SSH key of whoever launched it. To let another
person in, for example a second account on the same workstation, use
the **access registry**. It lives on the Proxmox cluster, in
`/etc/pve/pmox/`, so every node and every workstation sees the same
state. Only **public** keys ever go there. Private keys and API tokens
stay with their owner.

1. **Each person publishes their key once**, from their own account,
   after their own `pmox init`:

   ```
   bob$ pmox key publish
   ✓ published bob (SHA256:3f…) to pve → /etc/pve/pmox/keys/bob.pub
   ```

   The name defaults to the local username. Use `--name` to override it.
   With several servers configured, pmox asks which clusters to publish
   to; `--context` or `--all-contexts` skip the question.

2. **A cluster admin grants access**, interactively or from scripts:

   ```
   alice$ pmox access setup                   # People › VMs › Review, full-screen
   alice$ pmox access grant web1 db1 --to bob
   alice$ pmox access grant --all-vms --to carol   # every pmox VM, including future ones
   alice$ pmox access revoke web1 --to bob
   alice$ pmox access show                    # who can reach what, and whether each VM matches
   ```

3. **Bob connects as usual**: `pmox shell web1`.

pmox enforces grants by keeping a marked block in the VM user's
`~/.ssh/authorized_keys`:

```
# pmox-access begin (managed by pmox - edits inside this block are overwritten)
…
# pmox-access end
```

It writes that block through the QEMU guest agent's file API.
Everything outside the block, including the key the VM was launched
with, is left alone.

- **Stopped VMs** are reported as pending. `pmox access sync` brings
  them up to date later.
- **New VMs** get the keys of everyone with all-VM access as soon as
  `pmox launch` or `pmox vm clone` finishes.
- **A rejected key:** when a VM doesn't accept your key, `shell`,
  `exec`, `cp`, `sync`, `mount` and `apply` say whether you still need
  to publish, need to be granted, or just need a sync. They exit with
  code 10.
- **Privileges:** the token needs guest-agent file access on the VMs
  being shared (see [docs/pve-setup.md](docs/pve-setup.md)), plus node
  SSH as root to reach `/etc/pve`.
- **Checks and cleanup:** `pmox doctor` reports both, plus any VMs
  whose keys drifted from the registry. `pmox cleanup` removes grants
  for deleted VMs.

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
- **template** — pmox-generated templates (`ubuntu-…-pmox-…`, 9000–9099). **Destructive: this deletes VMs.** It is never selected by default — tick it in the checklist or pass `--include-templates` (or `--include template`).
- **vm** — pmox-tagged VMs missing the `pmox-ready` tag (set once a launch/clone completes, right before any post-create hook) — either abandoned mid-launch by an earlier failure, or (rarely) one still provisioning right now. **Destructive: this deletes VMs.** Also never selected by default — tick it in the checklist or pass `--include-vms` (or `--include vm`); review the listed VMs before removing.
- **context** — a configured server context, exactly what `pmox context delete` removes (config entry + keychain secret). Not leftover cruft — this is active configuration — so it's opt-in only: tick it in the checklist or pass `--include context`.
- **tack-config** — the entire `~/.config/pmox/tack/` directory: every playbook and role, hand-edited or scaffolded. Also active configuration, not cruft; opt-in only via the checklist or `--include tack-config`.

On a terminal, `pmox cleanup` shows a **checklist of every category it
checks**, not just the ones that found something — an empty one is
listed dimmed with "clean" instead of disappearing, so the
list stays a complete map of what cleanup covers; one with items shows
the count and is pre-checked (unless it's one of the four destructive
categories above, which start unchecked regardless). Then it asks
`Remove N item(s) now? [y/N]` — say `y` to delete right there, no need
to re-run with `--apply`. Non-interactively, scope with `--only`/`--skip`:

```
pmox cleanup                          # checklist, report, then y/N to remove now
pmox cleanup --apply                  # skip the checklist/prompt, remove unconditionally
pmox cleanup --only snippet,log       # just these
pmox cleanup --skip known-host        # everything safe except this
pmox cleanup --include-templates --apply   # also delete pmox templates
pmox cleanup --include-vms --apply         # also delete VMs abandoned mid-launch
pmox cleanup --include context,tack-config --apply   # full teardown of config + tack
pmox cleanup --output json
```

Aside from opted-in `template`/`vm` removal, VMs are never touched — use
`pmox delete`. `context` and `tack-config` are for a deliberate full
teardown, not routine cleanup — they remove active configuration, not
orphaned artifacts.

## Checking readiness

`pmox doctor` runs checks and tells you whether pmox is ready to launch
VMs — validating config, API reachability and token privileges, the
target node's online status, storage/template readiness, node SSH, and
local tooling. "Ready" means a bare `pmox launch <name>` will actually
work: every check that would block it is a hard failure, not a warning.

When a check fails or warns and pmox knows how to repair it, doctor
offers to fix it right there — confirmed one at a time — then
automatically re-runs every check afterward, so you see the problem is
actually gone instead of being told to re-run `pmox doctor` yourself:

```
pmox doctor            # offers fixes on a terminal; exits non-zero if any check fails
pmox doctor --strict   # also fail on warnings (good for CI)
pmox doctor -y         # apply offered fixes without asking (works without a terminal too)
pmox doctor --no-fix   # report only, don't offer anything
pmox doctor --output json | jq '.checks[] | select(.status=="fail")'
```

Currently fixable: rebuilding or converting a broken/missing template,
and enabling the guest agent. Rebuilding reuses `pmox template create`'s
own image/storage pickers, so it's only ever offered on a real
terminal, even with `-y`. Without a terminal and without `-y`, nothing
is ever offered or changed — doctor just reports, plus a one-line note
when something fixable exists. `--output json` never offers a fix.

Each check reports `✓ pass`, `! warn`, or `✗ fail` with an exact
remediation hint (e.g. the missing privilege name and the `pveum` line
to grant it, or the `pvesm set … snippets` command). Warnings don't
block readiness unless `--strict` is set. The exit code follows the
same taxonomy as other commands (unauthorized, network, not-found,
timeout, …), so CI can gate on it; `--output json` (with a stable
`schema_version` and per-check `id`) is the machine-readable form.
Add `--verbose` to also list passing checks.

**Away from home.**
- When the API host is on a private address and can't be reached,
  doctor's hint says why: this machine is off the home LAN with no
  Tailscale or NetBird route to it, or it's on a network with the same
  addresses as home.
- With `address: overlay`, the Network check `net.overlay` warns when
  this machine has no Tailscale or NetBird address.

See [docs/remote-access.md](./docs/remote-access.md).

## Cloud-init

pmox uploads a cloud-init file as a Proxmox `snippets` volume on
every `pmox launch` / `pmox vm clone` and points the new VM's
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
re-run `pmox init`), then run `pmox config cloud-init --regenerate`
to rewrite the cloud-init file with the new key.

## Setting up the VM from inside: yardo

Every VM pmox launches gets [yardo](https://github.com/eugenetaranov/yardo),
an interactive setup tool you run on the VM itself: `pmox shell web1`, then
`sudo yardo`. It installs and configures dev tools and languages, cloud, IaC
and Kubernetes CLIs, Docker, coding agents wired to an MCP gateway, system
hardening, SSH and git keys, and your dotfiles; it remembers your choices and
re-applies them. yardo is its own project: see its README for everything it
does. To use a VM's Docker or MCP gateway from your laptop, at home
or away, see [docs/remote-access.md](./docs/remote-access.md#8-docker-from-your-laptop).

Once a new VM answers SSH, pmox installs yardo's release on it (the VM
downloads it from GitHub and checks its checksum), before any
`--post-create`/`--tack` hook, so a hook can run `sudo yardo --yes`. It isn't
part of the user-data. A failed install only warns; with `--no-wait-ssh`
nothing is installed. pmox installs yardo's latest release (yardo is
released on its own; `PMOX_YARDO_VERSION=v0.1.2` installs a given one, and
`sudo yardo version up` upgrades it on a VM). To turn yardo off
for a server, set `yardo: false` under that server in `config.yaml`.

- `pmox yardo push web1` installs it, or refreshes it, on an existing VM;
  saved choices are kept.
- `pmox launch web1 --yardo-profile team.toml` (or `yardo_profile:` under a
  server) applies a yardo profile as soon as the VM is up, before any hook.
  A failed profile only warns, unless `--strict-hooks`.
- `pmox yardo apply [vm…] --profile team.toml` (or `--all`, or `--tag team`
  for the running pmox VMs with that Proxmox tag) installs yardo on each VM
  and applies the profiles there, four VMs at a time, with one result line
  per VM; without `--profile` it re-applies each VM's saved choices. `pmox
  yardo verify` with the same selectors changes nothing and lists what each
  VM is missing. Both exit non-zero if any VM failed.
- `pmox yardo gitkey web1 ~/.ssh/deploy_key [--host gitlab.example.com]`
  sends a private key for git to the VM (`sudo yardo gitkey` reads it on
  stdin); pmox keeps no copy.
- Your team's own yardo items in `~/.config/pmox/yardo/catalog.d/*.toml` and
  MCP server specs in `~/.config/pmox/yardo/mcp-catalog/*.spec` go with every
  install (launch, `push`, `apply`, `verify`) to `/etc/yardo/catalog.d/` and
  the VM's MCP catalog; the ones pmox copied before and you've since deleted
  are removed, and other files there are left alone.
- `pmox delete` offers to run `sudo yardo forget` on a running VM before
  deleting it (default yes): it removes the keys, logins and secrets yardo
  set up. `--forget` runs it without asking and `--no-forget` skips it.

Every VM's hostname is its name: pmox adds `hostname` (and `fqdn` for a dotted
name) and `manage_etc_hosts: true` to the cloud-init it uploads, unless your
cloud-init file sets a hostname itself.

pmox also adds a line to the VM's cloud-init that turns off
unattended-upgrades and the apt-daily timers: on first boot they hold the
dpkg lock and make installs fail. Security updates on these VMs are then up
to you (`sudo apt upgrade`), or tick "Automatic security updates" in yardo:
it turns them back on and leaves `/etc/yardo/auto-upgrades`, which tells the
boot-time line to stand down.

The starter cloud-init (`pmox init`, or `pmox config cloud-init --regenerate`)
carries the same `bootcmd` line, visible and editable, so it applies with
`yardo: false` too. To keep unattended upgrades, delete the line and set
`yardo: false`. An older cloud-init file doesn't have the line, or has it in
an older form; with yardo on, pmox swaps in the current line at launch;
otherwise regenerate the file.

The config keys `devbox_setup` and `devbox_profile` were renamed `yardo` and
`yardo_profile` when yardo was split out of pmox; pmox refuses a config that
still uses the old names, and says which to rename.

## Clock sync

Every launched VM's cloud-init also gets chrony, set to follow the Proxmox
host's clock through the KVM PTP device (`/dev/ptp0`). The clock is stepped
whenever it's more than a second off, so it's right again straight after a
snapshot rollback. Without the PTP device, chrony uses its default NTP pools.
Your cloud-init file is not changed. Turn it off per launch with
`--no-clock-sync`, or for a server with `clock_sync: false` under that server
in `config.yaml`.

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
  playbook.yaml     # default, used by `pmox apply <vm>` (or set one with --default)
  web.yaml          # a named profile: `pmox apply <vm> web`
  roles/            # local roles (or reference tack-roles remotely)
```

Run `pmox apply --init` to scaffold that layout with a starter playbook.
On a terminal it fetches the role list from
[tackhq/tack-roles](https://github.com/tackhq/tack-roles) and shows a
checklist (space to toggle, enter to confirm) so you pick what to
bootstrap with instead of getting a single hardcoded example role;
picking none scaffolds a bare starter with a commented example.
Non-interactively, or if the list can't be fetched, it scaffolds the
same fixed default as always.

**Playbooks from a git repo.** `pmox apply --init <url>` clones a repo
(`https://…`, `git@host:path`, `ssh://…`) into `~/.config/pmox/tack/`
instead. Any existing directory is first moved aside to
`tack.bak-<timestamp>`, after asking (or with `-y`). pmox then finds the
repo's playbooks: YAML files with a `hosts:` play, skipping `roles/`.
With exactly one, it becomes the default. With several, you pick the
default on a terminal; scripts get `site`/`playbook`/`main` if present.

```
pmox apply --init git@github.com:me/infra.git
pmox apply --default web        # change the default (no name: pick one)
pmox apply --update             # git pull; re-pick if the default vanished
pmox apply web1 playbooks/db    # run another playbook by name, once
```

The playbook is resolved in order:
1. `--playbook <path>`;
2. a playbook name argument (`web`, `playbooks/db`);
3. the playbook last used for that VM (remembered per VM);
4. the default (`pmox apply --default`, else `playbook.yaml`).

`launch --tack` uses the same default.

```
pmox apply web1                 # default or remembered profile
pmox apply web1 web             # ~/.config/pmox/tack/web.yaml (remembered)
pmox apply web1 --check         # plan only (tack --check)
pmox apply web1 -t docker       # only tasks tagged 'docker'
pmox apply web1 --playbook ./p.yaml
```

tack's own plan/apply confirmation is shown; pass `-y` (or
`PMOX_ASSUME_YES=1`) to auto-approve — `--output json` also
auto-approves, since there's no terminal to confirm on. **Host keys:**
tack verifies against `~/.ssh/known_hosts` (independently of pmox's own
`known_hosts_guests`) and has no equivalent of ssh's own
`-o UserKnownHostsFile`, so `pmox apply` pins an unknown key there
itself before invoking tack — the same trust-on-first-connect model
every other pmox SSH command already applies, just extended to the one
file tack actually reads. `--ssh-insecure` skips both tack's own
verification and this pinning. **Passwords:** every pmox-managed VM has
passwordless sudo (the cloud-init template grants it) and key-based
SSH, so pmox sets `TACK_SUDO_NO_PROMPT=1` / `TACK_SSH_NO_PROMPT=1` in
tack's environment (never a flag — nothing sensitive should ever land
in `ps` output) so it never blocks on a password prompt it doesn't
need. `pmox doctor` reports whether tack and a default playbook are
present.

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
    address: lan          # or overlay: connect to VMs at their Tailscale/NetBird address
    node_ssh:
      user: root
      auth: key
      key_path: /home/you/.ssh/id_ed25519
mount_excludes:
  - node_modules
  - "*.log"
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
pmox config cloud-init --regenerate    # rewrite the per-server cloud-init
```

`--regen-cloud-init` also lets you **pick a different SSH key** (it
defaults to the current one); a changed key is saved to config and
written into the cloud-init template. Re-running plain `pmox init`
and selecting a new key will likewise offer to regenerate the cloud-init
when it detects the file authorizes a different key. Either way, relaunch
existing VMs for a new key to take effect (cloud-init only runs at first
boot). `pmox doctor` flags this drift (`ssh_pubkey` vs the key in the
cloud-init file) before it turns into a `Permission denied (publickey)`.

**`address`** picks which of a VM's addresses pmox connects to:
- `lan` is the default: the VM's LAN address, even on a VM that is also
  on Tailscale or NetBird.
- `overlay` uses the VM's Tailscale (`tailscale0`) or NetBird (`wt0`)
  address whenever it has one, and the LAN address otherwise. That address
  is the same at home and away, so sessions and mounts survive moving
  between them.
  - `pmox list` marks overlay addresses ` (tailscale)` or ` (netbird)`, and
    `pmox vm info` shows which address pmox connects to.
  - When `shell` or `exec` starts a stopped VM, pmox waits up to 90s for
    its overlay address and connects to that.

See [docs/remote-access.md](./docs/remote-access.md).

### Contexts (multiple servers)

Each configured server is a **context** (kubectl-style), addressed by a
short name. When you have more than one, switch between them instead of
passing `--server` every time:

```
pmox context list             # table of contexts; current marked *
pmox context use prod         # switch the current context
pmox context current          # print the current context
pmox context rename 192.168.0.185 prod
pmox config edit prod                # change its defaults/access, no re-auth
pmox context delete lab       # forget a context + its secrets
pmox config path                     # print the config file location
```

A new context's name defaults to its host; `rename-context` gives it a
friendly name. Target a specific context for one command with `--context
<name>` (or `--server <name|url>`).

`pmox config edit [context]` is the one interactive command in this
group — it reopens the `pmox init` wizard for an already-configured
context, landing straight on the **Review** screen instead of redoing
the whole connection/token dance. The stored token is reused as-is;
reachability and the token are re-verified first (nothing re-typed) —
either failing points you at `pmox init` to fix the connection, since
repairing it isn't what `edit` is for. From Review you can jump back to
Defaults (node/template/storage/snippet-storage/bridge) or Access (SSH
key/user/node SSH) and change anything; every field starts pre-filled
with its current value instead of a blank re-discovery, and nothing is
written until you confirm. Omit `[context]` to pick one interactively
when more than one is configured.

Each command resolves its target in this order: `--server` (name or URL)
→ `--context` (name) → `PMOX_SERVER` → `PMOX_CONTEXT` → the current
context (`pmox context use`) → the only configured context → an interactive
picker. (`pmox context list` / `--remove` still work.)

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
| 9 | `ExitWarnings` | `pmox doctor --strict`: checks passed, but with warnings |
| 10 | `ExitSSHAuth` | a VM rejected your SSH key (see [Sharing VMs between users](#sharing-vms-between-users)) |

Scripts that wrap pmox can branch on these reliably; see
`internal/exitcode/exitcode.go` for the canonical definitions.

## Troubleshooting

### `pmox init` says "nothing responding at …" but the web UI loads fine

pmox's HTTP client honors `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`, same as
a browser or Go's own default client. If your network routes even
private-IP traffic through a proxy (common on managed/VPN'd machines)
and those env vars aren't set in the shell you're running `pmox` from,
the PVE host can be reachable in a browser yet unreachable to pmox.
Check `env | grep -i proxy` and set them if needed.

### `pmox init` says "no VMs visible on node …"

The token is missing `VM.Audit` on `/vms`. Grant it via the role or
disable privilege separation on the token. See
[docs/pve-setup.md](./docs/pve-setup.md#2-role-and-privileges).

### `pmox launch` times out waiting for an IP

The template was built without `qemu-guest-agent`, or the VM has
`agent: 0`. Rebuild the template with `pmox template create` or fix
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

Background mounts write a record and a log file under
`~/.local/state/pmox/mounts/`. `pmox mount list` shows them, `pmox umount`
offers them to stop, and `pmox umount --all <vm>` stops every mount for a
VM; inspect the log file to find out why a mount exited.

A running mount rides out sleep and network changes: a failed sync is
retried (5 s, doubling to 2 min) until the VM is reachable again, and
from the second failure on it asks Proxmox for the VM's current address.
A mount whose process ended (a reboot, or the VM or Proxmox unreachable
when it started) shows as `stale`: `pmox remount --stale` starts it again
with its options, and `pmox mount autostart on` does that for you at login
and every five minutes (a launchd agent on macOS, a systemd user timer on
Linux). `pmox remount <vm>:<path> --exclude=…` restarts a mount with
changed options.

### pmox can't reach the cluster away from home

Without Tailscale or NetBird, pmox only reaches the cluster from the
home LAN. `pmox doctor` names the cause (no route, or a network with the
same addresses as home);
[docs/remote-access.md](./docs/remote-access.md#10-troubleshooting) has
the fixes.

### Hook exited non-zero but `pmox launch` exited 0

That is the default — hook failure prints to stderr and returns
success, so the VM stays reachable for manual follow-up. Pass
`--strict-hooks` to upgrade hook failure to exit code 8.

## Development

```
task build             # ./bin/pmox
task test              # unit tests
task lint              # golangci-lint
task docs-check        # validate README/llms.txt/docs/examples links
```

The project layout mirrors [tackhq/tack](https://github.com/tackhq/tack):
commands under `cmd/pmox/`, logic under `internal/`, slices tracked
as OpenSpec proposals under `openspec/changes/`, shipped specs under
`openspec/specs/`, and the roadmap in
[ROADMAP.md](./ROADMAP.md).

## License

[MIT](./LICENSE).
