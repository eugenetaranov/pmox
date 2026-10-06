## Why

Several OS users on one workstation (for example `alice` for work and
`bob` for personal use) sometimes need to SSH into the same pmox VM.
That is impossible today. Every VM authorizes only the SSH key of the
local user who configured pmox when it was launched, and nothing
exists to add another person's key. The usual workarounds all weaken
the boundary between users:

- sharing one API token or config;
- copying a private key;
- running pmox with `sudo -u`.

pmox needs a supported way to grant access to a person by name, with
the list of who can reach what kept in one shared place.

## What Changes

- **New `pmox key publish` / `unpublish`.** Uploads the caller's
  public key to a shared registry on the Proxmox cluster, under the
  caller's local username. When more than one server is configured,
  it asks which Proxmox hosts to publish to. Only public keys are ever
  published; private keys and tokens never leave their owner.
- **New registry on Proxmox: `/etc/pve/pmox/`.** It holds
  `keys/<name>.pub` (published keys) and `access.yaml` (desired
  grants: person → specific VMs, or "all pmox VMs including future
  ones"). Because it lives on the cluster filesystem, it is copied to
  every node. pmox reaches it over the node SSH connection it already
  uses for snippet uploads.
- **New `pmox access` command:**
  - With no arguments it opens an interactive full-screen wizard
    (People › VMs › Review) built on the `config-wizard-app` shell.
  - Scripting equivalents: `access grant <vm…> --to <name> |
    --all-vms`, `access revoke`, `access list`, `access sync`.
- **Grants are enforced on the guest** by maintaining a marked block
  (`# pmox-access begin` … `# pmox-access end`) in the guest login
  user's `~/.ssh/authorized_keys`, through the QEMU guest agent's
  file-read/file-write API. Lines outside the block are never touched,
  and no `exec` (root-equivalent) privilege is needed. VMs that are
  stopped or unreachable are reported as pending, and `access sync`
  catches them up later.
- **`pmox launch`** pushes the keys of everyone granted "all pmox VMs"
  into a new VM once its guest agent answers. This is best-effort: a
  failure warns and never fails the launch.
- **SSH auth failures** from `shell` / `exec` / `cp` / `sync` /
  `mount` / `apply` get a dedicated message and exit code. The message
  points at `pmox key publish` and `pmox access`.
- **`pmox doctor`** checks the guest-agent file privileges needed for
  grants and flags registry drift (VMs whose keys differ from
  `access.yaml`). **`pmox cleanup`** offers to drop registry grants
  that point at VMs which no longer exist.

Out of scope:

- grant expiry (TTL);
- mirroring grants into Proxmox VM notes;
- SSH certificate authorities;
- reading another local user's files;
- sharing tokens between users.

## Capabilities

### New Capabilities

- `vm-access-registry`: publishing public keys to a cluster-side
  registry, managing who may reach which VMs (interactive and CLI),
  enforcing grants on guests through the guest agent, applying grants
  at launch, the SSH-failure guidance, and the doctor/cleanup checks
  that go with it.

### Modified Capabilities

None at the requirement level. Launch, the SSH-based commands, doctor
and cleanup gain behavior that the new capability specifies. Their
existing requirements are unchanged.

## Impact

- **New code:**
  - `cmd/pmox/key.go` and `cmd/pmox/access.go`;
  - `internal/accessreg` (registry read/write over SFTP);
  - `internal/guestkeys` (managed block in authorized_keys through the
    guest agent);
  - guest-agent file read/write calls in `internal/pveclient/agent.go`.
- **Touched:** `launch`, the SSH command error path, `internal/doctor`
  privilege checks, and `cleanup` categories.
- **Proxmox privileges** (beyond today's) on the VMs being shared:
  - PVE 9: `VM.GuestAgent.FileRead` and `VM.GuestAgent.FileWrite`;
  - PVE 8: `VM.Monitor`.
- **Node SSH:** writing `/etc/pve/pmox` requires a root node login,
  which is pmox's default.
- **Docs:** README section "Sharing VMs between users", plus
  `llms.txt`.
