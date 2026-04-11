## Why

After `launch-default` and `cloud-init-custom` ship, pmox can put a
VM on a cluster and reach it over SSH. But the typical first
question after "I have a VM" is "now install something on it" —
run a shell script, an Ansible playbook, or (since pmox is in
tack's ecosystem) a tack config. Cloud-init handles *first-boot*
provisioning, but post-boot orchestration is a better fit for
tools that are built for it.

This slice adds hooks that run **after** `pmox launch` completes
the SSH-wait phase. The hook contract is: we have an IP, we have
SSH access, hand off to a user-chosen tool. Three shorthands
cover the common cases (`--post-create <script>`, `--tack`,
`--ansible`) and `--strict-hooks` upgrades hook failure from
warning to error (default: hook failure does not fail the
launch).

## What Changes

- Add `--post-create <path>` to `pmox launch` and `pmox clone`.
  The path is a local shell script that receives the VM's IP,
  VMID, and name as env vars (`PMOX_IP`, `PMOX_VMID`, `PMOX_NAME`).
  The launcher invokes it directly (`exec.CommandContext`, not
  `sh -c`), streaming stdout/stderr to the user.
- Add `--tack <config-path>`: runs `tack apply --host <ip> <config>`
  as the post-create step. Requires `tack` to be on `$PATH`; if
  missing, the hook fails and (by default) prints a warning.
- Add `--ansible <playbook-path>`: runs
  `ansible-playbook -i <ip>, -u <user> --private-key <key> <playbook>`.
  Requires `ansible-playbook` on `$PATH`.
- Add `--strict-hooks`: upgrades hook failure from warning-and-
  exit-0 to error-and-exit-nonzero (`ExitHook`, a new exit code).
  Without it, a failed hook leaves the VM running and reachable,
  and the launch command exits 0 with the error printed to stderr.
  With it, the command exits with the new `ExitHook` code.
- Add `ExitHook` to `internal/exitcode` (existing from slice 1).
  Resolves the parked-thread question "--strict-hooks exit code
  semantics": hook failure always prints the error; `--strict-hooks`
  just changes the exit code.
- Only one of `--post-create`, `--tack`, `--ansible` may be
  passed. They are mutually exclusive — if the user combines two,
  the launcher errors before any API call with a clear message
  naming both flags.
- Hooks run **only after** wait-SSH succeeds. If `--no-wait-ssh`
  is set, hooks are skipped entirely (can't run a command against
  a VM you haven't verified is reachable), and a stderr warning
  is printed if both `--no-wait-ssh` and a hook flag were passed.
- Hook execution respects the overall `--wait` budget. If
  wait-IP and wait-SSH consumed all of it, hooks get a minimum
  30s timeout anyway — strict timeout would be useless if the
  user wanted to run a 5-minute Ansible playbook.

## Capabilities

### New Capabilities
- `post-create-hooks`: the three hook flag surfaces, mutual
  exclusion, env-var contract, strict vs lenient failure mode,
  `ExitHook` exit code, and the shell-free direct exec.

### Modified Capabilities
- `launch-default`: `launch.Run` gains a phase 10 after wait-SSH
  that invokes the hook (if any). `launch.Options` gains a `Hook`
  field (an interface or struct describing the chosen hook).
- `list-info-lifecycle`: `pmox clone` picks up the same three
  flags, same semantics.

## Impact

- **New files**: `internal/hook/hook.go` (the `Hook` interface,
  the three implementations, env-var setup, timeout handling),
  `internal/hook/hook_test.go`, `internal/hook/strict_test.go`.
- **Modified files**: `internal/launch/launch.go` — append phase
  10 after wait-SSH. `cmd/pmox/launch.go` and `cmd/pmox/clone.go`
  — add the four new flags, build a `Hook` implementation, wire
  into `Options`. `internal/exitcode/exitcode.go` — add
  `ExitHook = 7` (or next available code).
- **New dependencies**: none. `os/exec` from stdlib.
- **Cross-slice contract**: the `PMOX_IP` / `PMOX_VMID` /
  `PMOX_NAME` env var names are frozen here. Slice 9's README
  documents them.
