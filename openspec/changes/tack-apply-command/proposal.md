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
- **SSH handoff via a synthesized inventory**: pmox writes a throwaway
  `0600` tack inventory containing the VM's `ssh: {user, key}` and runs
  `tack run <playbook> -i <tmp-inventory> --hosts <vm>`. This works even
  when pmox's key is not in the ssh-agent or `~/.ssh/config` (tack only
  accepts a key via inventory or ssh config, never a CLI flag).
- **Plan/apply passthrough**: tack's own plan/apply confirmation shows
  through pmox; `-y` / `PMOX_ASSUME_YES` adds tack's `--auto-approve`;
  `--check` maps to tack `--check`. `--tags`/`--skip-tags` pass through.
- **Per-VM profile memory**: the profile used for a VM is remembered in a
  local state file keyed by server+vmid (mirroring the mount registry),
  so a later bare `pmox apply <vm>` re-applies it.
- **Fix + unify the launch `--tack` hook**: correct `tack apply` →
  `tack run … -i <tmp-inventory>`, route it through the same apply code
  path, and let `--tack` (no path) default to the config playbook.
- **`pmox apply --init`** scaffolds a starter `~/.config/pmox/tack/`
  (a `playbook.yaml` referencing a couple of tack-roles) so first run is
  friendly instead of erroring.
- **doctor check**: report whether `tack` is on PATH and a playbook is
  resolvable.
- **Host-key verification task**: determine tack's real host-key behavior
  (it is undocumented) and either seed pmox's pinned guest key into the
  inventory or expose a `--ssh-insecure` passthrough; document the result.

## Capabilities

### New Capabilities

- `tack-apply` — the `pmox apply` command, playbook resolution ladder,
  synthesized-inventory SSH handoff, plan/apply passthrough, per-VM
  profile memory, `--init` scaffold, the unified/ fixed launch `--tack`
  behavior, and the doctor readiness check.

## Impact

- New `cmd/pmox/apply.go` (command wiring, resolution ladder, tack
  invocation) and registration in `main.go` help grouping.
- New `internal/tack` (build the argv, write/cleanup the temp inventory,
  classify "tack not installed") and `internal/tackprofile` (per-VM
  profile state file under `~/.local/state/pmox/tack/`).
- `internal/hook/hook.go` — `TackHook` rewritten to `tack run` via the
  shared invocation.
- `cmd/pmox/doctor.go` — add the tack readiness check.
- Docs: README + llms.txt (new command, `~/.config/pmox/tack/` layout).
- Tests: resolution ladder, inventory synthesis, argv building, profile
  memory, hook fix.
