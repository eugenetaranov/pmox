## Context

Every VM-targeted pmox command today reaches a `vm.Resolve(ctx, client, arg)` call after Cobra has already enforced `cobra.ExactArgs(1)` (or `ExactArgs(2)` for `clone`). If the user omits the positional argument, Cobra fails before our code runs, with a generic "accepts 1 arg(s)" message. The user then has to run `pmox list`, pick a name, and re-invoke.

pmox already ships a TUI layer at `internal/tui` built on `charmbracelet/huh`. It exports:

- `tui.SelectOne(title, opts, fallback) string` — arrow-key single-select with no filter, SIGINT-on-abort, and a short-circuit that returns the lone value when there's only one option.
- `tui.StdinIsTerminal() bool` — the TTY probe, already used by the confirm helper.
- `tui.NewTTYConfirmer` — the y/N confirmer used by `pmox delete`.

`internal/vm` owns `Resolve`, `Ref`, and the pmox-tag check (`HasPMOXTag`). The cluster scan used by `pmox list` goes through `pveclient.ClusterResources`, which returns the same struct slice that populates the list view.

The current "list pmox VMs" path is `cmd/pmox/list.go`, which re-filters `ClusterResources` by tag before rendering. That filter is the source of truth for "what counts as a selectable target."

## Goals / Non-Goals

**Goals:**
- Making the target argument *optional* on every single-target command (`shell`, `exec`, `start`, `stop`, `info`, `delete`).
- When absent and stdin+stderr are TTYs, present an interactive picker populated by the existing `ClusterResources`-filtered-by-`pmox`-tag set.
- When absent and exactly one pmox VM exists, silently auto-select it (no prompt, no extra output on stdout — a one-line hint to stderr is fine).
- When absent and no pmox VMs exist, print a friendly "no pmox VMs found — run `pmox launch` first" and exit non-zero.
- When absent and stdin or stderr is not a TTY (pipes, CI), fall back to the existing usage error so scripts stay deterministic.
- Preserve every current code path for "target supplied explicitly" — the picker is never invoked when the user typed a name or VMID.

**Non-Goals:**
- Two-positional commands (`clone`, `cp`, `sync`, `mount`) are *not* in scope for the picker. They already have ambiguous "which arg is the target" questions, and wiring the picker in for the degenerate no-args case is a separate, smaller change.
- No new flags, no `--interactive` toggle, no env var. TTY detection is the only gate.
- No fuzzy filtering, no multi-select, no keyboard shortcuts beyond what `huh.Select` already provides.
- No change to `--force` semantics. The picker lists pmox-tagged VMs only; `--force` without a positional arg still requires the user to type the target (same error as today) — wiring `--force` into the picker to show untagged VMs is a follow-up.

## Decisions

### Decision 1: New helper lives in `internal/vm` as `vm.Pick` rather than a new package

Reuse the existing package because:
- `vm.Resolve`, `vm.Ref`, and `vm.HasPMOXTag` are already here, and `Pick` conceptually returns a `*Ref`.
- Adding a new `internal/targetpicker` package would force `vm` to import it or force both to depend on a shared type — circular. Keeping `Pick` next to `Resolve` avoids that.
- Tests already have a `pvetest` fake for `ClusterResources` that `vm` tests consume. Adding `Pick` here lets us reuse that fixture.

**Signature:**
```go
// Pick returns a single pmox-tagged VM. When exactly one exists, it is
// returned without prompting. When multiple exist and stdin+stderr are
// TTYs, an interactive picker is shown. Otherwise an error is returned
// with a message appropriate for the context (no VMs / non-TTY / user
// aborted).
func Pick(ctx context.Context, client *pveclient.Client, stderr io.Writer) (*Ref, error)
```

**Alternative considered:** pass a `PickOptions{TTY: func() bool, Prompt: func(...) string}` struct to make it test-friendly. Rejected — we can inject `tui.StdinIsTerminal` and `tui.SelectOne` as package-level function vars (same pattern `cmd/pmox/ssh.go` already uses with `sshExecFn`/`sshRunFn`). Keeps the public API tiny.

### Decision 2: Cobra `Args` becomes `cobra.MaximumNArgs(1)` for single-target commands

Change every single-target command from `cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)`. Inside the `RunE`, if `len(args) == 0`, call `vm.Pick`; otherwise call `vm.Resolve(args[0])` as today.

**Alternative considered:** leave `ExactArgs(1)` and intercept the error from Cobra's validation. Rejected — intercepting Cobra's built-in arg errors is fragile and makes the help text lie ("accepts 1 arg" when it really accepts 0 or 1).

For `pmox exec`, the existing flow is already `cobra.MinimumNArgs(1)` because of the `-- <command>` tail. We need a distinct adjustment there: the VM-argument-optional mode only applies when the first positional is NOT `--` and no positional appears before `--`. The existing `os.Args` scan for `--` stays; we add a branch that calls `Pick` when no positional exists *before* the `--`.

### Decision 3: TTY gate checks BOTH stdin AND stderr, not just stdin

Stdin-only TTY detection is wrong: `pmox shell < /dev/tty` (piped stdout) would still show the picker but pollute piped output. `huh` draws on stderr-ish TTY output (actually /dev/tty), but for consistency with our existing `tui.StdinIsTerminal` check plus a new `tui.StderrIsTerminal`, we gate on both. If either side isn't a terminal, the command errors with the same "missing argument" message it would show today (Cobra's default), preserving scriptability.

### Decision 4: Auto-single-VM is silent, not confirmed

When exactly one pmox VM exists, `Pick` returns it with no prompt and no extra stdout. It writes a single stderr line (`Selected <name> (vmid N)\n`) for verbose mode only. This matches the "in the common case there is only one VM" motivation and avoids a redundant confirmation step. The user can always verify with `--dry-run` (future) or by reading the command's own progress output.

**Alternative considered:** always show the picker, even for one VM. Rejected — contradicts the user's explicit spec ("if target is just one — use it, without showing a list") and adds friction.

### Decision 5: Picker rows show `name (vmid, node, status, ip)`

Use the same columns `pmox list` uses, squashed into a single line per `huh.Option`. The `Key` (displayed label) is the pretty string; the `Value` (returned by `SelectOne`) is the `vmid` as a string, which `vm.Resolve` can consume verbatim.

Example row:
```
smoke (100, p0, running, 192.168.0.207)
```

Stopped VMs show status `stopped` and IP blank. The picker renders them because `shell`/`exec` auto-start stopped VMs anyway.

## Risks / Trade-offs

- **[Risk]** `ClusterResources` can be slow on large clusters → adds latency before the picker appears. **Mitigation:** `pmox list` already performs the same call and is considered fast enough; we're not adding a new API call, just reusing the existing one. No caching layer in scope.

- **[Risk]** `huh` does a full-screen take-over on the alternate screen buffer. If a user pipes the output of `pmox shell` to a log file while running in a terminal, the TTY check protects us — but if both stdin and stderr happen to be TTYs while stdout is redirected, the picker still runs correctly because `huh` writes to /dev/tty. Confirmed by the existing `pmox delete` y/N flow which works under the same conditions.

- **[Risk]** Introducing optional positional args may confuse users who read the help text and expect `<name|vmid>` to be required. **Mitigation:** update the `Short`/`Long` help to say `<name|vmid>` is optional ("If omitted, pmox shows a picker") and add a scenario in the spec.

- **[Trade-off]** We skip two-positional commands for now. Users still have to type `pmox cp <src> web1:/path` and can't get a picker for the `web1` half. Acceptable because the shape of those commands already requires the user to know the remote path — picking the VM doesn't save the keystroke battle the single-target commands face.

- **[Risk]** `vm.Pick` reaches into `tui` at test time. **Mitigation:** expose the two TUI dependencies (`isTTY func() bool`, `selectOne func(title string, opts []huh.Option[string]) string`) as package-level function vars in the `vm` package, defaulting to `tui.StdinIsTerminal` and a small adapter over `tui.SelectOne`. Tests override both to drive deterministic behavior.

## Migration Plan

No migration required. Every existing invocation with an explicit target works unchanged. The only observable change is that invocations that previously errored with "accepts 1 arg(s)" now either succeed via the picker (TTY) or error with the same message (non-TTY). The non-TTY error text stays byte-identical so scripts don't break.
