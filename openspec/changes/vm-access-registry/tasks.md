## 1. Verify platform assumptions

- [x] 1.1 On a real PVE 8/9 node, confirm root over SFTP can create `/etc/pve/pmox/keys/x.pub`, write `access.yaml` via temp file + rename, and read both back from another node; record limits (file size) in design.md, or switch to the fallback directory
- [x] 1.2 Confirm guest-agent `file-read` / `file-write` on an Ubuntu pmox VM: truncating write keeps owner/mode of `~/.ssh/authorized_keys`, sshd accepts the result; note the exact privileges on PVE 8 and PVE 9

## 2. Proxmox client and guest keys

- [x] 2.1 `pveclient`: `AgentFileRead(ctx, node, vmid, path)` (base64 decode, truncated flag) and `AgentFileWrite(ctx, node, vmid, path, content)`, with typed errors for 403, agent not running, VM stopped
- [x] 2.2 `internal/guestkeys`: parse `/etc/passwd` for a user's home; render/replace the managed block (pure functions); `Apply(ctx, agent, user, keys) (Result, error)` that reads, diffs, writes only when changed; refuse a missing authorized_keys
- [x] 2.3 Unit tests: block insert/replace/remove, lines outside preserved, idempotence, CRLF and missing trailing newline, passwd parsing; pvetest-backed tests for the agent calls

## 3. Registry

- [x] 3.1 `internal/accessreg`: types for `access.yaml` (version 1), published key files with header metadata; read/write over a small SFTP interface (pvessh) with atomic write and hash-checked retry-once
- [x] 3.2 Desired-state computation: key set per VMID from all-VM grantees + listed VMIDs, skipping unpublished names with a warning
- [x] 3.3 Tests with an in-memory SFTP fake: round-trip, concurrent-modification retry and conflict, malformed file errors

## 4. `pmox key`

- [x] 4.1 `pmox key publish [--name] [--context …] [--replace]`: resolve local username, read configured public key, pick contexts (multi-select when >1, flags otherwise), confirm before replacing a different key, write via node SSH
- [x] 4.2 `pmox key unpublish`, `pmox key show` (prints own public key + fingerprint and where it is published)
- [x] 4.3 Tests: default name, replace confirmation, non-TTY `--replace` requirement, multi-context selection

## 5. `pmox access` CLI

- [x] 5.1 `access grant|revoke <vm…> --to <name> | --all-vms`: update registry, then apply to affected VMs with bounded concurrency; per-VM ✓ / = / pending output; exit non-zero on non-stop failures
- [x] 5.2 `access list [vm]` (desired vs actual, marking drift) and `access sync [vm…|--all]`
- [x] 5.3 Tests with stubbed registry and agent: grant/revoke/list/sync outcomes, unknown name, stopped VM pending, 403 message per PVE version

## 6. Interactive `pmox access`

- [x] 6.1 Wizard stages on `internal/tui/wizard`: context picker (when >1), People (multi-select, empty-state guidance), VMs per person (pre-checked current grants, ★ all-VMs option), Review (+/− diff, Apply/Edit/Cancel), Apply (spinner + per-VM results in the summary)
- [x] 6.2 Harness tests for each stage and an end-to-end teatest flow

## 7. Launch, SSH guidance, doctor, cleanup

- [x] 7.1 Launch/clone: apply registry grants for the new VM after the IP is known; warn on failure, silent when no registry
- [x] 7.2 SSH commands: detect publickey rejection (exit 255 + stderr), print guidance (published vs not), add `exitcode.ErrAuth`
- [x] 7.3 doctor: optional "VM access sharing" privilege probe, registry reachability, drift warning
- [x] 7.4 cleanup: `access-grant` category (stale VMIDs) and opt-in destructive `access-key` (published keys with no grants)

## 8. Docs and verification

- [x] 8.1 README "Sharing VMs between users" section and llms.txt entries; required privileges in docs/pve-setup.md
- [x] 8.2 `go vet`, `task lint`, `task test`, `openspec validate vm-access-registry`
- [ ] 8.3 Manual end-to-end on a real cluster with two local users: publish, interactive grant, shell as the second user, revoke, launch with an all-VM grantee
  - Done single-user on PVE 9.1.1 (scratch VM): publish, grant, guest block written (owner/mode kept), list in sync, doctor 3/3, revoke removes block, rejected-key guidance exits 10; found and fixed a non-ASCII file-write bug. Remaining: a second OS user, interactive grant, all-VM grant at launch.
