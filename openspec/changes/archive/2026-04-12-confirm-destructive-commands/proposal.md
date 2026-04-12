## Why

`pmox delete` currently destroys a VM the moment the command is invoked — no
confirmation prompt, no dry-run, no second chance. A typo on a name argument or
a scripted loop run against the wrong server resolves directly into a stop +
destroy. The pmox tag check protects hand-managed VMs but does nothing to stop
a user from wiping the wrong pmox-launched VM. Other CLIs in the same niche
(multipass, vagrant, gh, kubectl) prompt before irreversible actions; pmox
should match that baseline.

## What Changes

- `pmox delete` SHALL print a one-line summary of the resolved VM (name, VMID,
  node, status, tags) and require an interactive `y/N` confirmation before
  issuing any `Shutdown`, `Stop`, or `Delete` API call. Default is **No**.
- A new `--yes` / `-y` flag SHALL skip the confirmation prompt for scripted
  and CI use. The flag SHALL also be honored via `PMOX_ASSUME_YES=1` so loops
  in shell scripts don't have to thread the flag through every invocation.
- When stdin is not a TTY and `--yes` is not set, the command SHALL refuse to
  proceed and exit non-zero with an error directing the user to pass `--yes`
  for non-interactive use. This prevents the prompt from being silently
  swallowed by a pipe or cron run.
- `--force` SHALL continue to mean "bypass the pmox tag check + use hard stop"
  and SHALL be orthogonal to `--yes`. Using `--force` alone still prompts;
  using `--yes --force` is the explicit "I know what I'm doing" combination.
- The confirmation infrastructure SHALL live in a shared helper
  (`internal/tui/confirm` or similar) so future destructive commands
  (e.g. a hypothetical `pmox destroy-template`) can reuse it.

## Capabilities

### New Capabilities
- `interactive-confirmation`: shared helper for y/N prompts, TTY detection,
  and `--yes` / `PMOX_ASSUME_YES` handling. Owned by `internal/tui` so any
  command that needs a destructive-action gate uses one consistent UX.

### Modified Capabilities
- `delete-command`: add confirmation requirement, `--yes` flag, non-TTY
  refusal, and the new interaction with `--force`.

## Impact

- **Code**: `cmd/pmox/delete.go`, `cmd/pmox/delete_test.go`, new helper under
  `internal/tui/`, possibly a small change to `cmd/pmox/main.go` if the
  `PMOX_ASSUME_YES` env binding lives there.
- **UX**: scripts that piped to `pmox delete` without expecting interaction
  will start failing fast — this is intentional. Release notes / README must
  call out the `--yes` / `PMOX_ASSUME_YES` migration path.
- **Tests**: existing delete tests already drive `executeDelete` directly
  with a fake client; the confirmation gate must be testable the same way
  (inject a `confirmer` seam, default to a real TTY confirmer in production).
- **No PVE API changes.** No config schema changes.
