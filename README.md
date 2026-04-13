# pmox

pmox is a multipass-style CLI for Proxmox VE. This repo is under construction; see `openspec/changes/` for in-flight work.

Working subcommands include `pmox configure`, `pmox launch`, `pmox delete`, `pmox list`, `pmox info`, `pmox start`, `pmox stop`, `pmox clone`, and `pmox create-template`. Single-target commands (`shell`, `exec`, `delete`, `start`, `stop`, `info`) accept an optional `[name|vmid]` — omit it to auto-select the only pmox VM or pick from an interactive list. `pmox mount` makes the `<name|vmid>:` prefix on the destination optional (e.g. `pmox mount ./src /opt/app`) and falls back to the same picker, and `pmox umount` with no arguments resolves a VM via the picker and stops every mount for it.

## Configuring a server

Run `pmox configure` and answer the prompts. You'll need:

- **API URL** — the base URL of your Proxmox VE host, e.g. `https://192.168.0.185:8006`. You can also paste the web UI URL (`https://host:8006/#v1:0:...`); everything after the port is stripped.
- **API token ID** — in the form `user@realm!tokenname`, e.g. `root@pam!pmox` or `pmox@pve!mytoken`.
- **API token secret** — the UUID shown once when the token is created.
- **Node SSH credentials** — pmox also prompts for an SSH username (default `root`) and either a password or private key. These are required by `pmox create-template`, which uploads a cloud-init snippet directly into the storage pool's `snippets/` directory via SFTP. The secret is stored in the OS keyring alongside the API token; the YAML config only records the username, auth mode, and (for key mode) the key file path. On first contact, pmox prints the host's SSH fingerprint and asks you to confirm (TOFU), then pins it to `~/.config/pmox/known_hosts` — pmox never reads or writes `~/.ssh/known_hosts`.
- **SSH public key** — pmox scans `~/.ssh/*.pub` and asks you to pick one. The selected **path** is stored in `config.yaml` as `ssh_pubkey`; the key material is only ever read at cloud-init render time.
- **Default user** — the login name baked into the cloud-init template (default `ubuntu`).

At the end of `pmox configure`, pmox writes a starter cloud-init file to `~/.config/pmox/cloud-init/<host>-<port>.yaml` with your selected user and public key substituted in. If the file already exists it is left alone (edit in place; never clobbered by reruns).

## Cloud-init

Every `pmox launch` and `pmox clone` uploads the per-server cloud-init file as a Proxmox `snippets` volume and points the new VM's `cicustom` at it. There is no built-in cloud-init mode anymore — the file on disk is the single source of truth for what ships to the VM.

Per-server file: `~/.config/pmox/cloud-init/<host>-<port>.yaml` (one per configured PVE server; the slug comes from the canonical API URL).

**Editing it.** The starter template sets up a sudo user, injects your SSH key, installs `qemu-guest-agent`, and enables it. Anything else cloud-init supports — extra packages, `runcmd`, write_files, network config — you add by editing this file directly. Changes take effect on the next `pmox launch` or `pmox clone`; running VMs are not updated.

**Rotating the SSH key.** The public key is read fresh on every render, so:

1. update `ssh_pubkey:` in `~/.config/pmox/config.yaml` (or re-run `pmox configure`) to point at the new `.pub` file, then
2. run `pmox configure --regen-cloud-init` to rewrite the cloud-init file with the new key. Pmox prompts before overwriting.

**If the file gets deleted.** `pmox launch` refuses to run and tells you to rerun `pmox configure --regen-cloud-init`, which writes a fresh default without prompting when no file exists.

**Snippet cleanup.** `pmox delete` removes the per-VM snippet (`pmox-<vmid>-user-data.yaml`) from the PVE host after the VM is destroyed. Legacy VMs created before this change have no `cicustom` set and skip the cleanup silently.

### Flags / environment

| Flag | Env var | Effect |
| --- | --- | --- |
| `--ssh-insecure` | `PMOX_SSH_INSECURE=1` | Skip SSH host-key verification for the process. Intended for scripted / first-bootstrap use; emits a stderr warning on first use. |
| `--yes` / `-y` | `PMOX_ASSUME_YES=1` | Skip interactive confirmation prompts (currently used by `pmox delete`). Required for non-interactive / scripted use. |

### Security tradeoffs

`create-template` requires SSH because the Proxmox API's upload endpoint rejects `content=snippets` on stock PVE 8.x, and the alternative (mutating the cluster-wide storage `content=` list) is a worse side effect. pmox offers both password and key-file auth. Password auth keeps the cleartext root password in the OS keyring — noticeably weaker than a scoped API token, but matches the "I don't manage SSH keys on my PVE host" workflow many home-lab users asked for. Prefer key auth when you can; pmox will read an unencrypted or passphrase-protected private key from disk and only the passphrase (not the key material) ever touches the keyring.

### Creating an API token in Proxmox

1. In the Proxmox web UI, go to **Datacenter → Permissions → API Tokens**.
2. Click **Add**, pick a user (e.g. `root@pam`, or a dedicated `pmox@pve` user), and set a Token ID (e.g. `pmox`). Uncheck **Privilege Separation** if you want the token to inherit the user's permissions directly.
3. Copy the full token ID (`user@realm!tokenname`) and the secret — the secret is shown only once.

### Required permissions

The token needs to see nodes, VMs, storage pools, and network bridges during `configure`, and later needs to create/clone VMs. The simplest working setup is to create a dedicated user (e.g. `pmox@pve`) and grant it a custom role with:

| Privilege             | Needed on     | Why                                     |
| --------------------- | ------------- | --------------------------------------- |
| `Sys.Audit`           | `/`           | list nodes, read node network bridges   |
| `VM.Audit`            | `/vms`        | list VMs and discover templates         |
| `VM.Clone`            | `/vms`        | clone a template into a new VM          |
| `VM.Allocate`         | `/vms`        | create new VMs                          |
| `VM.Config.*`         | `/vms`        | set cores, memory, disk, cloud-init     |
| `VM.PowerMgmt`        | `/vms`        | start/stop the new VM                   |
| `Datastore.Audit`     | `/storage`    | list storage pools                      |
| `Datastore.AllocateSpace` | `/storage/<pool>` | allocate a disk on the target pool |
| `Datastore.Allocate`  | `/storage/<pool>` | `pmox create-template` enabling `snippets` content on a storage pool |
| `SDN.Use`             | `/sdn/zones/localnetwork` | attach NICs to bridges      |

Quick path (if you're happy using `root@pam`): in **Datacenter → Permissions → API Tokens**, click **Add**, pick `root@pam`, name the token, and **uncheck Privilege Separation**. The token then inherits root's full rights and no extra role assignment is needed.

If `pmox configure` shows `no VMs visible on node …` or `could not list storage …`, it means the token is missing `VM.Audit` or `Datastore.Audit` respectively — fix the role or disable privilege separation on the token.

## Connecting to a VM

### `pmox shell`

`pmox shell [name|vmid]` opens an interactive SSH session to a pmox-managed VM. If the VM is stopped, it auto-starts and waits for SSH readiness before connecting. If the argument is omitted, pmox auto-selects the only pmox VM when one exists, or shows an interactive picker when there are several.

```bash
pmox shell                     # auto-select single VM / show picker
pmox shell web1
pmox shell --user ubuntu web1
pmox shell --identity ~/.ssh/custom_key web1
```

### `pmox exec`

`pmox exec [name|vmid] -- <command> [args...]` runs a single command on a VM over SSH and returns its output and exit code. The VM argument is optional and falls back to the picker when omitted.

```bash
pmox exec -- uname -a          # auto-select single VM / show picker
pmox exec web1 -- uname -a
pmox exec web1 -- cat /etc/hostname
```

Both commands default to user `pmox` and derive the private key from the configured SSH public key (stripping `.pub`). Use `--user` / `-u` and `--identity` / `-i` to override. Both enforce the pmox tag check; pass `--force` to connect to untagged VMs.

Guest VM host keys are not verified (`StrictHostKeyChecking=no`) since VMs are ephemeral and keys change on every launch.

## Deleting a VM

`pmox delete [name|vmid]` stops and destroys a VM on the resolved cluster. If the argument is omitted, pmox auto-selects the only pmox VM when one exists, or shows an interactive picker when there are several; the y/N confirmation still runs against the picked VM.

### Confirmation

Before issuing any destructive API call, `pmox delete` prints a summary of the resolved VM and asks for an interactive `y/N` confirmation (default No). This prevents accidental deletion from a typo or a scripted loop targeting the wrong server.

To skip the prompt for scripted or CI use, pass `--yes` / `-y` or set `PMOX_ASSUME_YES=1`:

```bash
pmox delete --yes web1
# or
PMOX_ASSUME_YES=1 pmox delete web1
```

When stdin is not a TTY and neither `--yes` nor `PMOX_ASSUME_YES` is set, the command refuses to proceed and exits non-zero. This prevents the prompt from being silently swallowed by a pipe or cron job.

**Migration note for scripted callers:** if you have scripts that call `pmox delete` without user interaction, add `--yes` or set `PMOX_ASSUME_YES=1` — they will now fail without it.

`--force` and `--yes` are orthogonal: `--force` bypasses the tag check and uses hard stop; `--yes` skips the confirmation prompt. Use `--yes --force` for fully non-interactive force-delete.

## Creating a template

`pmox create-template` builds a ready-to-launch Proxmox template from an Ubuntu cloud image interactively. It fetches Canonical's simplestreams catalogue, lets you pick a release and target storage, downloads the image via PVE's `download-url`, boots a throw-away VM with a cloud-init snippet that installs `qemu-guest-agent` and cleans machine IDs, waits for the guest to power itself off, detaches the cloud-init drive, and converts the VM to a template in the 9000–9099 VMID range. The command requires PVE 8.0+ and an interactive TTY. See the slice spec at `openspec/specs/create-template/spec.md` for the full state machine and error contract.
