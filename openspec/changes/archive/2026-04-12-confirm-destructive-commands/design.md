## Context

`pmox delete` is the only command in the current CLI surface that can
irreversibly destroy user state. Today it skips straight from argument
resolution to `Shutdown` / `Stop` / `Delete`, with no second chance for
the operator. The pmox tag check guards against accidentally touching
hand-managed VMs but does nothing once a VMID is in `pmox`'s blast radius.

The codebase already has an `internal/tui` package (currently just
`picker.go` for `SelectOne` prompts used by `pmox configure`). Existing
delete tests drive `executeDelete` directly with a fake `pveclient` and a
buffered stderr — any new confirmation gate needs to be testable through
that same seam, not by spawning a real PTY.

## Goals / Non-Goals

**Goals:**
- Force a deliberate human Y/N before any destructive PVE call from `pmox delete`.
- Provide a single, scriptable bypass (`--yes` flag + `PMOX_ASSUME_YES` env).
- Refuse to proceed silently when stdin is non-interactive and no bypass is set.
- Land the confirmation logic in a shared helper so future destructive
  commands inherit the same UX without copy-paste.
- Keep the existing `executeDelete` test seam working — every new code path
  must be drivable by the existing fake-client tests.

**Non-Goals:**
- A general-purpose "operation log" or undo system.
- Confirmation for non-destructive commands (`stop` is reversible — left alone
  for this slice; `clone`, `info`, `list` are non-destructive).
- Adding confirmation to `pmox launch`'s clone phase (creation, not destruction).
- Rich TUI prompts (huh, bubbletea forms). A plain `y/N` line read is enough.
- A `--dry-run` mode for delete. Out of scope; can be a follow-up if needed.

## Decisions

### D1 — Helper lives in `internal/tui`, exposes a `Confirmer` seam

Add `internal/tui/confirm.go` with:

```go
// Confirmer asks the operator a yes/no question. Implementations decide
// where prompts come from (real stdin, scripted bypass, fake in tests).
type Confirmer interface {
    Confirm(ctx context.Context, prompt string) (bool, error)
}
```

Plus two concrete implementations:

- `NewTTYConfirmer(in io.Reader, out io.Writer)` — reads a single line from
  `in`, treats `y`/`yes` (case-insensitive, trimmed) as approval, everything
  else (including empty input → default No) as denial. Errors only on I/O
  failures.
- `AlwaysConfirmer{}` — returns `(true, nil)` unconditionally. Used when
  `--yes` / `PMOX_ASSUME_YES` is set.

`runDelete` constructs the right `Confirmer` based on flag/env state and
passes it into `executeDelete`. Tests inject a hand-rolled fake.

**Alternative considered:** a single `Confirm(prompt, opts...)` function with
no interface. Rejected because the existing delete tests already use
dependency injection on the client; matching that pattern keeps the test
shape consistent with `cmd/pmox/delete_test.go`.

### D2 — Non-TTY without `--yes` is a hard failure, not a silent allow

When stdin is not a TTY (`!term.IsTerminal(int(os.Stdin.Fd()))`) and neither
`--yes` nor `PMOX_ASSUME_YES` is set, the command exits non-zero with:

```
refusing to delete VM "web1" (vmid 104): stdin is not a TTY and --yes was
not passed; re-run with --yes (or PMOX_ASSUME_YES=1) for non-interactive use
```

**Why fail-closed instead of fail-open or auto-yes:** auto-yes turns every
piped invocation into an unconditional destroy — exactly what this proposal
exists to prevent. Auto-no would deadlock interactive users who pipe input
intentionally. Failing fast forces the operator to make a deliberate choice
about scripted use, exactly once.

**Alternative considered:** prompt anyway and treat EOF as No. Rejected
because EOF on a closed stdin is indistinguishable from "user pressed Enter
to take the default" and gives no signal to the operator that confirmation
was attempted.

### D3 — Confirmation runs AFTER resolve and tag check, BEFORE GetStatus

Order of operations in `executeDelete`:

1. `vm.Resolve` → name/VMID → reference (no PVE writes)
2. Tag check (`HasPMOXTag` unless `--force`)
3. **Confirmation prompt** (NEW)
4. `GetStatus`
5. `Shutdown` / `Stop` (if running)
6. `Delete`

Rationale: the prompt needs to display a useful summary (name, VMID, node,
tags), so it has to run after `Resolve`. It must run before any state-changing
PVE call. Putting it after `GetStatus` would mean a prompt followed by a long
delay if status is slow — putting it before is snappier and still has all the
data the prompt needs from `Resolve`. The status gets re-checked during the
shutdown branch anyway.

The summary line printed before the prompt is:

```
About to delete VM "web1" (vmid 104, node pve, tags pmox)
Continue? [y/N]:
```

If `--force` is set, the line is amended:

```
About to FORCE-delete VM "web1" (vmid 104, node pve, tags <none>)
This will use hard stop (no graceful shutdown) and bypasses the pmox tag check.
Continue? [y/N]:
```

### D4 — `--yes` and `--force` are orthogonal

`--force` keeps its existing meaning (bypass tag check + hard stop).
`--yes` is a new, independent axis (skip confirmation). The four combinations:

| flags | tag check | confirm | stop verb |
| --- | --- | --- | --- |
| (none) | enforced | required | shutdown |
| `--yes` | enforced | skipped | shutdown |
| `--force` | bypassed | required | stop |
| `--yes --force` | bypassed | skipped | stop |

The "I really mean it, no questions" combo is `--yes --force`. This matches
how `kubectl delete --force` and `gh repo delete --yes --confirm` work in
adjacent tools.

### D5 — Env var name: `PMOX_ASSUME_YES`

Match the GNU/`apt`/`debconf` convention (`DEBIAN_FRONTEND=noninteractive`,
`APT_ASSUME_YES`) rather than `PMOX_YES`. The longer name is more obviously
a behavior switch when grep'd in scripts. Parse using the existing
`envBool` helper (already used by `PMOX_SSH_INSECURE`), so `1`, `true`, `yes`
(case-insensitive) all enable.

## Risks / Trade-offs

- **[Risk]** Existing scripts that pipe `pmox delete` (e.g. `for id in ...; do
  pmox delete $id; done` in CI) will start failing. → **Mitigation:** call out
  prominently in README + commit message + (eventual) release notes; the fix
  is `--yes` or `PMOX_ASSUME_YES=1`. Failing loudly is preferable to silently
  preserving a footgun.
- **[Risk]** TTY detection is platform-flaky (e.g. Windows consoles, weird
  terminal multiplexers). → **Mitigation:** pmox is darwin/linux only per the
  project conventions, and `golang.org/x/term.IsTerminal` is reliable on both.
  No new dependency.
- **[Risk]** A user could `--yes` on autopilot and still nuke the wrong VM.
  → **Mitigation:** out of scope for this slice. The prompt is the speed bump;
  habituated bypass is a human-factors problem the CLI can't fully solve.
- **[Trade-off]** No `--dry-run` mode. A second slice could add it; for now
  the prompt itself prints what would happen, which covers the common
  "what's this going to do?" use case.

## Migration Plan

1. Land the helper, the gate, the tests, and the README note in one slice.
2. README's `pmox delete` section gains a "Confirmation" subsection.
3. The commit message explicitly mentions that scripted callers must add
   `--yes` or set `PMOX_ASSUME_YES=1` and links to the README section.
4. No version-bump or migration shim — pmox is pre-1.0 and the existing
   delete behavior is the bug being fixed, not a contract being broken.
