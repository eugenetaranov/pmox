## Context

pmox keeps everything per local OS user:

- config and the token secret;
- the SSH key;
- known_hosts;
- per-VM identity records.

A VM launched by `alice` authorizes only alice's key, which is baked in
by her per-server cloud-init file. `bob` on the same workstation can run
his own `pmox init` and get API access, but he has no way into alice's
VMs.

Design inputs came from three reviews: CLI UX, implementation, and
sysadmin/security. The user then made these decisions:

- no file handoff between local accounts;
- a registry kept on the Proxmox side rather than the workstation;
- an interactive "people × VMs" setup alongside CLI flags;
- people identified by their local username;
- anyone with admin (node) access may edit grants;
- no TTL;
- no mirror of grants into Proxmox VM notes.

## Goals / Non-Goals

**Goals:**

- Share a VM with another person by name, using only public keys.
- One registry per cluster, visible from every node and from every
  workstation that can reach it.
- Least privilege on the guest: no root-equivalent guest-agent exec.
- Works on VMs launched before this feature existed.
- Interactive and scriptable.

**Non-Goals:**

- Sharing tokens or config between local users.
- Reading other users' files.
- Grant expiry.
- An SSH CA.
- Windows guests.
- Granting to guest users other than the VM's login user.

## Decisions

### D1. Registry at `/etc/pve/pmox/` over node SSH

```
/etc/pve/pmox/
  keys/<name>.pub     # one OpenSSH public-key line; header comment with
                      # published-by host, local uid, timestamp, PVE token user
  access.yaml         # desired state
```

```yaml
version: 1
people:
  bob:
    all_vms: false
    vms: [101, 102]     # VMIDs, which are stable across renames
  carol:
    all_vms: true
```

pmox reads and writes the registry over the SFTP connection `pvessh`
already opens for snippet uploads. `/etc/pve` is pmxcfs, so it is
copied cluster-wide with no extra work.

- **Writes:** `access.yaml` is written atomically (temp file plus
  rename within `/etc/pve/pmox`).
- **Concurrent edits:** a writer re-reads the file and compares its
  hash before replacing it. On a mismatch it retries the read-merge
  once, then reports a conflict.
- **Trust:** whoever can write `/etc/pve` is already a cluster admin,
  so the registry adds no new trust boundary. This matches the user's
  "admins are fine".
- **Alternatives considered:**
  - Snippet storage. Rejected: it is per-node unless the storage is
    shared, and its contents are visible through the storage API.
  - PVE user comment fields. Rejected as a hack, size-limited, and it
    needs `User.Modify`.
- **Verified (task 1.1, PVE on `p0`, root over SFTP):**
  - creating directories under `/etc/pve`;
  - writing new files;
  - atomic replace via a dot-temp plus rename;
  - a 66 KB `access.yaml`;
  - listing, removing files, and removing empty directories.

  pmxcfs needs no chmod and rejects nothing here. The fallback
  directory is not needed.

### D2. Identity is the local username

`pmox key publish` names the key after `os/user.Current().Username`.
`--name` overrides it, for example when the same person uses different
usernames on two machines.

- If a *different* key is already published under that name, pmox
  shows both fingerprints and asks before replacing it. With no TTY it
  requires `--replace`.
- The key file's header records the publishing host, the token user
  and the time, for audit.

### D3. Guest enforcement: a marked block in authorized_keys via guest-agent file I/O

```
<launch key from cloud-init>
# pmox-access begin (managed by pmox - edits inside this block are overwritten)
ssh-ed25519 AAAA… pmox-access:bob
ssh-ed25519 AAAA… pmox-access:carol
# pmox-access end
```

Steps:

1. Read `/etc/passwd` with `agent/file-read` to find the login user's
   home directory (the user comes from the per-VM identity record,
   else the server's `user`).
2. Read `<home>/.ssh/authorized_keys`.
3. Replace or insert the block.
4. Write the file back with `agent/file-write`.

The guest agent opens an existing file for truncating write, so its
owner (the user) and mode (0600, set by cloud-init) are kept.

**Missing file** (the user has no keys yet): pmox refuses and reports
it, rather than creating a root-owned file that sshd would reject under
StrictModes. This doesn't happen on pmox-launched VMs.

**Why not:**

- *An sshd `AuthorizedKeysFile` drop-in plus `/etc/ssh/pmox_keys`.*
  Existing VMs would need exec to install it and reload sshd, and
  pmox-launched VMs would need cloud-init edits that conflict with
  users' full-replace custom cloud-init.
- *Guest-agent exec.* It needs `VM.GuestAgent.Unrestricted`, which is
  root-equivalent and which the security review rejected.

**Verified (task 1.2, PVE 9.1.1, Ubuntu 26.04 guest from a pmox
template):**

- `agent/file-read` returns `/etc/passwd` and `authorized_keys`
  decoded.
- `agent/file-write` with plain content round-trips byte for byte.
- After the write, `~/.ssh/authorized_keys` is still `ubuntu:ubuntu
  0600` and `~/.ssh` is `0700`.
- sshd accepts a key that sits only in the managed block. A login with
  it succeeded.

**Privileges:**

- PVE 9: `VM.GuestAgent.FileRead` and `VM.GuestAgent.FileWrite`.
- PVE 8: `VM.Monitor`.
- A 403 maps to an error that names the missing privilege, plus a
  `pveum` hint.

### D4. Reconciliation model

`access.yaml` is the desired state. `access sync` computes each VM's
key set:

- everyone with `all_vms`;
- plus everyone whose `vms` list includes the VM;
- minus people whose key isn't published (warned).

It then rewrites only the managed block of VMs whose block differs.
`grant` and `revoke` update `access.yaml`, then sync just the affected
VMs.

**Outcome per VM:** ✓ updated, = unchanged, or pending, with the
reason: stopped, agent not responding, not a pmox VM, or 403. The
command exits non-zero if any VM failed for a reason other than being
stopped. Stopped VMs are only reported as pending and catch up on the
next sync or launch.

### D5. Interactive `pmox access`

`pmox access` reuses the `internal/tui/wizard` shell: full-screen, with
a frame, async ops and dialogs.

- **People:** a multi-select of published names, showing fingerprint
  and published-from.
- **VMs:** a multi-select of pmox-tagged VMs (name, VMID, status), plus
  a "★ All pmox VMs, including future ones" option. This page is shown
  once per selected person, with the person's current grants
  pre-checked.
- **Review:** a +/− diff of the changes, then Apply / Edit people /
  Edit VMs / Cancel.
- **Apply** writes `access.yaml`, then shows sync progress per VM with
  ✓ / pending and the reason.

With more than one context configured, a context picker runs first (or
`--context`). With nothing published yet, the People page explains
`pmox key publish` instead of showing an empty list.

### D6. Launch integration

After a successful launch (the IP is known, so the guest agent is up),
pmox computes the key set for the new VM and applies it as in D4. This
covers everyone with `all_vms`, and also people whose `vms` list names
the VMID, for example after a VMID is reused.

It is best-effort:

- a failure prints `warning: could not apply shared access to <vm>:
  <err> — run 'pmox access sync <vm>'`;
- a missing registry is a silent no-op;
- it never fails the launch.

### D7. SSH auth-failure guidance

The SSH-based commands (shell, exec, cp, sync, mount, apply) already
run ssh. When ssh exits with 255 and its stderr says `Permission denied
(publickey`, pmox prints:

```
<vm> doesn't accept your key ~/.ssh/pmox_ed25519 (SHA256:…) as <user>.
  Publish your key once:   pmox key publish
  Then a cluster admin grants it:   pmox access grant <vm> --to <you>
```

The command exits with a new `exitcode.ErrAuth`. If the caller's key is
already published but not granted for this VM, the first line instead
says it is published and not granted.

### D8. doctor and cleanup

- **doctor:**
  - a guest-agent file-privilege probe, as an optional check named "VM
    access sharing";
  - registry reachability;
  - drift: VMs whose managed block differs from the desired state, as
    a warning with the `pmox access sync` hint.
- **cleanup:** a new non-destructive category, `access-grant`. It
  removes VMIDs in `access.yaml` that no longer exist on the cluster,
  plus published keys whose names have no grants. Removing keys is
  destructive and opt-in.

## Risks / Trade-offs

- [pmxcfs refuses some operations] → verify in task 1.1. Fallback
  directory with a replication warning.
- [Concurrent registry edits from two workstations] → hash-checked
  write with one retry; otherwise report the conflict.
- [A user hand-edits inside the managed block] → those lines are
  overwritten. The block header says so, and lines outside the block
  are preserved.
- [Home directory not under /home, e.g. root or LDAP users] → resolved
  from `/etc/passwd` via file-read. LDAP-only users are unsupported and
  reported.
- [SELinux contexts on rewrite] → a truncating write keeps the inode
  and its label. Ubuntu-only guests are the primary target anyway.
- [Large clusters] → sync runs per VM with bounded concurrency (4) and
  reports progress.
- [A key published to the wrong cluster] → publish shows the target
  hosts and asks first. `unpublish` removes the key.

## Migration Plan

Additive. Nothing changes for users who never publish a key. VMs from
before this change are handled by the same mechanism (D3). Rollback is
reverting the change. The registry files can be left on the cluster, or
removed with `rm -r /etc/pve/pmox`.

## Open Questions

- Should `pmox access` also let people grant themselves when their own
  token has the file privileges, without a cluster admin? Proposed: yes.
  It is the same operation, and the API decides whether it's allowed.
