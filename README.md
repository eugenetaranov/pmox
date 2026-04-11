# pmox

pmox is a multipass-style CLI for Proxmox VE. This repo is under construction; see `openspec/changes/` for in-flight work.

Currently the only working subcommand is `pmox configure`.

## Configuring a server

Run `pmox configure` and answer the prompts. You'll need:

- **API URL** — the base URL of your Proxmox VE host, e.g. `https://192.168.0.185:8006`. You can also paste the web UI URL (`https://host:8006/#v1:0:...`); everything after the port is stripped.
- **API token ID** — in the form `user@realm!tokenname`, e.g. `root@pam!pmox` or `pmox@pve!mytoken`.
- **API token secret** — the UUID shown once when the token is created.

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
| `SDN.Use`             | `/sdn/zones/localnetwork` | attach NICs to bridges      |

Quick path (if you're happy using `root@pam`): in **Datacenter → Permissions → API Tokens**, click **Add**, pick `root@pam`, name the token, and **uncheck Privilege Separation**. The token then inherits root's full rights and no extra role assignment is needed.

If `pmox configure` shows `no VMs visible on node …` or `could not list storage …`, it means the token is missing `VM.Audit` or `Datastore.Audit` respectively — fix the role or disable privilege separation on the token.
