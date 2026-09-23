## Why

pmox creates VMs; tack (the companion tool from tackhq/tack) configures
them. Today the only bridge is the `--tack` post-create hook, and that
hook is **broken**: it shells out to `tack apply --host <ip> --user
<user> <config>`, but modern tack has no `apply` subcommand — it is
`tack run <playbook> -c ssh://user@host`. There is also no way to
(re-)apply configuration to an *existing* VM (day-2 convergence). Users
want a first-class `pmox apply <vm>` that runs their tack playbook/roles
(kept under `~/.config/pmox/tack/`) against a VM, reusing the SSH user
and key pmox already knows.

## What Changes

- **New command `pmox apply [vm]`** — resolves the target (arg optional →
  picker), auto-starts a stopped VM and waits for its IP, then runs tack
  against it. Multi-VM/fleet is explicitly out of scope for v1.
- **Playbook resolution ladder**: `--playbook <path>` → profile arg
  (`~/.config/pmox/tack/<profile>.yaml`) → the profile last used for that
  VM (remembered) → default `~/.config/pmox/tack/playbook.yaml`. Roles
  live in `~/.config/pmox/tack/roles/` or are referenced remotely from a
  playbook (e.g. `tack-roles.git//docker`).
- **SSH handoff via tack's connection flags**: pmox runs
  `tack run <playbook> -c ssh://<user>@<ip> --ssh-key <identity>`, passing
  the VM's user and identity pmox already resolves (mirrors the existing
  `AnsibleHook --private-key`). tack verifies host keys against
  `~/.ssh/known_hosts` (fail-closed); pmox's `--ssh-insecure` /
  `PMOX_SSH_INSECURE` maps through to tack's `--ssh-insecure`.
- **Plan/apply passthrough**: tack's own plan/apply confirmation shows
  through pmox; `-y` / `PMOX_ASSUME_YES` adds tack's `--auto-approve`;
  `--check` maps to tack `--check`. `--tags`/`--skip-tags` pass through.
- **Per-VM profile memory**: the profile used for a VM is remembered in a
  local state file keyed by server+vmid (mirroring the mount registry),
  so a later bare `pmox apply <vm>` re-applies it.
- **Fix + unify the launch `--tack` hook**: correct `tack apply` →
  `tack run … -c ssh://user@ip --ssh-key <identity>`, route it through the
  same apply code path, and let `--tack` (no path) default to the config
  playbook.
- **`pmox apply --init`** scaffolds a starter `~/.config/pmox/tack/`
  (a `playbook.yaml` referencing a couple of tack-roles) so first run is
  friendly instead of erroring.
- **doctor check**: report whether `tack` is on PATH and a playbook is
  resolvable.
- **Host-key verification**: tack verifies against `~/.ssh/known_hosts`
  (fail-closed) with a `--ssh-insecure` escape; there is no custom
  known_hosts path, so this is independent of pmox's
  `known_hosts_guests`. pmox maps `--ssh-insecure` through and documents
  the `~/.ssh/known_hosts` dependency (first apply to a brand-new VM may
  need the key scanned or `--ssh-insecure`).

## Capabilities

### New Capabilities

- `tack-apply` — the `pmox apply` command, playbook resolution ladder,
  synthesized-inventory SSH handoff, plan/apply passthrough, per-VM
  profile memory, `--init` scaffold, the unified/ fixed launch `--tack`
  behavior, and the doctor readiness check.

## Impact

- New `cmd/pmox/apply.go` (command wiring, resolution ladder, tack
  invocation) and registration in `main.go` help grouping.
- New `internal/tack` (build the `tack run` argv incl. connection/ssh
  flags, classify "tack not installed") and `internal/tackprofile`
  (per-VM profile state file under `~/.local/state/pmox/tack/`).
- `internal/hook/hook.go` — `TackHook` rewritten to `tack run` via the
  shared invocation.
- `cmd/pmox/doctor.go` — add the tack readiness check.
- Docs: README + llms.txt (new command, `~/.config/pmox/tack/` layout).
- Tests: resolution ladder, inventory synthesis, argv building, profile
  memory, hook fix.
