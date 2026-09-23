## Context

pmox launches/manages VMs; tack configures them. The existing bridge is
the `--tack` launch hook (`internal/hook/hook.go:72`), which runs
`tack apply --host <ip> --user <user> <config>`. Current tack has no
`apply` subcommand — the CLI is `tack run <playbook>` with connection via
`-c ssh://user@host:port` or an inventory file, and a private key can be
supplied **only** through an inventory (`ssh: {user, key}`) or
`~/.ssh/config`, never a CLI flag. tack's host-key verification behavior
is undocumented.

pmox already resolves a VM's IP (guest agent), SSH user (server config →
default), and identity, and pins guest host keys via TOFU in
`~/.config/pmox/known_hosts_guests`.

## Goals / Non-Goals

**Goals:**
- `pmox apply <vm>` runs the user's tack playbook against a VM reusing
  pmox's SSH resolution, with no manual inventory authoring.
- Sensible playbook selection with minimal typing (the ladder).
- Fix the broken launch `--tack` hook and share one invocation path.
- Keep tack's plan/apply UX intact.

**Non-Goals:**
- Fleet apply (`--all`, by-tag, `--forks` fan-out) — deferred.
- Reimplementing tack features (vault, generate, scaffold).
- A `-c ssh://` connection mode — the synthesized inventory is the sole
  mechanism in v1 (may add `-c` later).

## Decisions

### 1. Command shape
`pmox apply [vm]` — arg optional (→ picker, like other single-target
commands). Auto-starts a stopped VM and waits for its IP (as `pmox shell`
does) before invoking tack. Single target in v1.

### 2. Playbook resolution ladder
In order, first match wins:
1. `--playbook <path>` (explicit; `~` expanded).
2. profile positional arg → `~/.config/pmox/tack/<profile>.yaml`.
3. remembered profile for this VM (state file) → its
   `~/.config/pmox/tack/<profile>.yaml`.
4. default `~/.config/pmox/tack/playbook.yaml`.
If the resolved file is missing, print a helpful message pointing at
`--init`, not a raw tack error.

### 3. SSH handoff via tack's connection flags
Modern tack (`tack run`) exposes `-c ssh://user@host:port`, `--ssh-user`,
`--ssh-key`, `--ssh-port`, `--ssh-insecure`, `--hosts`, `--tags`/`-t`,
`--skip-tags`, `--check` (alias of `--dry-run`), and `--auto-approve`/`-a`
(env `TACK_AUTO_APPROVE`). So pmox invokes:
```
tack run <playbook> -c ssh://<user>@<ip> --ssh-key <identity> \
     [--ssh-insecure] [--check] [-t <tags>] [--skip-tags <tags>] \
     [--auto-approve] [--output json]
```
This needs no temp file and mirrors the existing `AnsibleHook`
(`--private-key`). (Earlier drafts synthesized an inventory on the
assumption tack took a key only via inventory; the CLI flags make that
unnecessary.)

### 4. Plan/apply passthrough
Do not wrap tack's confirmation. Wire tack's stdin/stdout/stderr to the
terminal so its plan/apply prompt shows through. `-y` /
`PMOX_ASSUME_YES=1` adds `--auto-approve`; `--check` adds tack `--check`
(plan only). `--output json` passes through for CI.

### 5. Per-VM profile memory
A local JSON state file under `~/.local/state/pmox/tack/` (0700),
mirroring the mount registry. Keyed by `server URL + vmid` so it survives
renames and can be pruned when a VM is deleted (a future `cleanup`
extension). Written only when a profile arg is used; a bare `pmox apply
<vm>` reads it. `--playbook` does not update the remembered profile.

### 6. Fix + unify the launch `--tack` hook
Rewrite `TackHook` to build the same argv as `pmox apply` (via a shared
`internal/tack` helper): `tack run <playbook> -c ssh://<user>@<ip>
--ssh-key <identity>`. `--tack` with no value defaults to the config
playbook. Post-create keeps `--strict-hooks`/`ExitHook` semantics. The
hook's `Env.SSHKey` already carries the identity.

### 7. `--init` scaffold
`pmox apply --init` creates `~/.config/pmox/tack/` with a starter
`playbook.yaml` (references a couple of tack-roles, e.g. docker) and a
`roles/` dir. Never overwrites existing files.

### 8. Host-key verification (resolved from tack source)
tack's SSH connector verifies against `~/.ssh/known_hosts` **fail-closed**
(a missing/mismatched key aborts), with a `--ssh-insecure` flag (env
`TACK_SSH_INSECURE`) to skip. There is no option for a custom known_hosts
path, so tack cannot be pointed at pmox's `known_hosts_guests`. Therefore:
pmox maps its own `--ssh-insecure` / `PMOX_SSH_INSECURE` to tack's
`--ssh-insecure`, and documents that tack uses `~/.ssh/known_hosts`
independently — the first apply to a brand-new VM may require scanning the
key (`ssh-keyscan -H <ip> >> ~/.ssh/known_hosts`) or `--ssh-insecure`.
(Seeding pmox's pin into `~/.ssh/known_hosts` automatically is a possible
future enhancement, deliberately out of scope here.)

### 9. doctor check
Add a `config`-phase check: `tack` on PATH (warn if absent — apply is
optional) and a resolvable default playbook (info/warn). Never runs tack.

## Risks / Trade-offs

- **tack CLI drift**: we depend on `tack run` flags. Mitigation: a single
  `internal/tack` builder + a doctor check; classify "tack not found"
  clearly.
- **Host-key gap**: if tack can't be pointed at pmox's pin, `pmox apply`
  may verify differently than the rest of pmox. Mitigation: Decision 8's
  investigation + explicit `--ssh-insecure` + docs.
- **Temp inventory leakage**: it contains a key *path* (not the key) and
  is `0600`, deleted on exit. Low risk.
- **State file staleness**: remembered profiles for deleted VMs linger
  until a cleanup pass. Low impact (keyed by vmid).
