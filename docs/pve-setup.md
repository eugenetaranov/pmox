# Proxmox VE setup for pmox

pmox talks to a Proxmox VE cluster via the PVE HTTP API (for VM
operations) and via SSH/SFTP (for cloud-init snippet upload, used by
`pmox create-template` and every `pmox launch` / `pmox clone`). This
page walks through preparing a PVE host so that `pmox configure`
succeeds on the first try.

- [1. API token](#1-api-token)
- [2. Role and privileges](#2-role-and-privileges)
- [3. Node SSH access](#3-node-ssh-access)
- [4. Template preparation](#4-template-preparation)
- [5. Common first-launch errors](#5-common-first-launch-errors)

## 1. API token

In the PVE web UI: **Datacenter → Permissions → API Tokens → Add**.

- Pick a user. `root@pam` is simplest; a dedicated `pmox@pve` user
  is cleaner.
- Set a Token ID (e.g. `pmox`). The full token ID is
  `user@realm!tokenname`, for example `root@pam!pmox`.
- Uncheck **Privilege Separation** if you want the token to inherit
  the user's permissions directly. Otherwise you must assign
  permissions to the *token* explicitly in step 2.
- Copy the secret — PVE only shows it once.

CLI equivalent:

```
pveum user token add root@pam pmox --privsep 0
```

## 2. Role and privileges

If you unchecked Privilege Separation on a `root@pam` token, you're
already done — root has everything. Otherwise, create a role and
grant it the following privileges:

| Privilege                 | Path                        | Why                                                          |
| ------------------------- | --------------------------- | ------------------------------------------------------------ |
| `Sys.Audit`               | `/`                         | list nodes, read network bridges                             |
| `VM.Audit`                | `/vms`                      | list VMs, discover templates                                 |
| `VM.Allocate`             | `/vms`                      | create new VMs                                               |
| `VM.Clone`                | `/vms`                      | clone a template into a new VM                               |
| `VM.Config.*`             | `/vms`                      | set cores, memory, disk, cloud-init                          |
| `VM.PowerMgmt`            | `/vms`                      | start and stop VMs                                           |
| `Datastore.Audit`         | `/storage`                  | list storage pools                                           |
| `Datastore.AllocateSpace` | `/storage/<pool>`           | allocate a disk on the target pool                           |
| `Datastore.Allocate`      | `/storage/<pool>`           | `pmox create-template` enabling `snippets` content on a pool |
| `SDN.Use`                 | `/sdn/zones/localnetwork`   | attach NICs to bridges                                       |

Create the role and assign it:

```
pveum role add PmoxRole -privs "Sys.Audit VM.Audit VM.Allocate VM.Clone VM.Config.Disk VM.Config.CPU VM.Config.Memory VM.Config.Network VM.Config.Options VM.Config.Cloudinit VM.PowerMgmt Datastore.Audit Datastore.AllocateSpace Datastore.Allocate SDN.Use"
pveum acl modify / -token 'pmox@pve!pmox' -role PmoxRole
```

Adjust the token ID and role scope as needed. Datastore privileges
only need to apply to the storage pools you actually use.

## 3. Node SSH access

Proxmox's HTTP upload endpoint hard-codes a rejection of
`content=snippets`, so pmox uploads cloud-init files over SSH/SFTP.
`pmox configure` asks for a Linux user on the PVE node (default
`root`) plus either a password or a private key, and validates both
with a live handshake before writing the config.

- **Password mode**: pmox stores the password in the OS keyring.
  Matches the "I don't manage SSH keys on my PVE host" workflow;
  weaker than key auth.
- **Key mode**: pmox stores only the path to the private key in
  `config.yaml`, reads the key material at upload time, and keeps
  any passphrase in the keyring.

On the first connection pmox prints the host's SSH fingerprint and
asks you to pin it to `~/.config/pmox/known_hosts`. pmox never reads
or writes `~/.ssh/known_hosts`.

## 4. Template preparation

pmox launches VMs by cloning a template. A template that works with
pmox needs three things:

1. **`qemu-guest-agent` installed inside the image.** pmox polls the
   agent for the VM's IPv4 address after launch; without it, every
   launch hangs on IP discovery and times out.

   ```
   apt-get install -y qemu-guest-agent
   systemctl enable --now qemu-guest-agent
   ```

2. **`agent: 1` set on the template.** This toggles PVE's agent RPC
   on for cloned VMs. Set it once on the template:

   ```
   qm set <template-vmid> --agent 1
   ```

3. **A cloud-init drive attached.** PVE's cloud-init drive is how
   pmox delivers the per-server cloud-init file; without it, the
   `cicustom` volume points at nothing.

The easiest path is to let `pmox create-template` do all three for
you — it downloads an Ubuntu cloud image, bakes `qemu-guest-agent`
in via a one-shot cloud-init run, and converts the result into a
template in the 9000–9099 VMID range. Requires PVE 8.0+ and an
interactive TTY.

## 5. Common first-launch errors

### `401 Unauthorized` / `403 Forbidden`

The token is missing a privilege from the table above. Re-run
`pmox configure` in `--debug` mode to see exactly which API call
failed, then grant the matching privilege to the token's user or
role.

### `qemu-guest-agent not responding` / IP never appears

The template is missing `qemu-guest-agent` or has `agent: 0`. Fix
the template per section 4 and rebuild any VMs cloned from it.

### `storage does not have 'snippets' in its content types`

Proxmox storage pools gate what content types they hold; the
default `local` storage permits snippets, but custom directory or
NFS pools may not. Either pick a different pool with
`--snippet-storage`, or enable snippets on the current pool:

```
pvesm set <pool> --content images,iso,vztmpl,rootdir,snippets
```

`pmox configure` can enable this for you on a directory-backed
pool when you grant it `Datastore.Allocate`.

### `ssh: handshake failed` during snippet upload

Either the node SSH credentials are wrong, or the host key pinned
in `~/.config/pmox/known_hosts` does not match the current host key
(e.g. after a reinstall). Delete the stale line from that file and
re-run the command; pmox will re-pin on the next connection.

See the [cloud-init section of the README](../README.md#cloud-init)
for the user-data format pmox ships and how to customise it.
