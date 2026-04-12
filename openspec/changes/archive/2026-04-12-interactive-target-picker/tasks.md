## 1. Shared picker helper in `internal/vm`

- [x] 1.1 Add `tui.StderrIsTerminal` alongside the existing `StdinIsTerminal` var in `internal/tui`, backed by `term.IsTerminal(int(os.Stderr.Fd()))`.
- [x] 1.2 Create `internal/vm/pick.go` defining `func Pick(ctx, client, stderr io.Writer) (*Ref, error)` plus package-level `isStdinTTY`, `isStderrTTY`, and `selectOne` function vars that default to the TUI helpers.
- [x] 1.3 Implement `Pick` using the same `ClusterResources` + `HasPMOXTag` filter that `cmd/pmox/list.go` uses (factor out a small `listPMOXVMs` helper if needed to avoid duplication with `list.go`).
- [x] 1.4 Apply the selection rules: zero VMs → friendly error, one VM → return silently, multi + TTY → `SelectOne`, multi + non-TTY → error with the Cobra-style missing-argument message.
- [x] 1.5 Format picker rows as `<name> (<vmid>, <node>, <status>, <ip-or-blank>)` and set the `huh.Option.Value` to the vmid as a string so callers can feed it back into `vm.Resolve`.
- [x] 1.6 Write table-driven tests in `internal/vm/pick_test.go` covering: zero VMs, one VM silent auto-select, multi + TTY select, multi + non-TTY error, user abort. Override `isStdinTTY`/`isStderrTTY`/`selectOne` in each case.

## 2. Wire single-target commands to the picker

- [x] 2.1 `cmd/pmox/ssh.go`: change `newShellCmd` Args from `cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)`; update `runShell` to call `vm.Pick` when `len(args) == 0`, then feed the returned `*vm.Ref` into the existing `resolveSSHTarget` path (used `resolveTargetArg` helper in delete.go that returns vmid-as-string for Resolve).
- [x] 2.2 `cmd/pmox/ssh.go`: update `newExecCmd` so that the VM positional is optional. Scan `os.Args` for `--` as today, but compute the VM argument from the args *before* `--` via `cmd.ArgsLenAtDash()`; if none exist, call `vm.Pick`. Keep the empty-remote-command error intact.
- [x] 2.3 `cmd/pmox/delete.go`: change Args from `cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)`; when `len(args) == 0`, call `vm.Pick` before the confirmation prompt. Ensure the existing y/N flow still runs against the picked VM.
- [x] 2.4 `cmd/pmox/start.go`, `cmd/pmox/stop.go`, `cmd/pmox/info.go`: same treatment — `MaximumNArgs(1)` + `vm.Pick` fallback.
- [x] 2.5 Update each command's `Short` and `Long` help to reflect the now-optional positional (`<name|vmid>` → `[name|vmid]`) and a one-line note about the picker.
- [x] 2.6 Leave `clone`, `cp`, `sync`, `mount` unchanged for this pass (documented non-goal in design.md).

## 3. Command-level tests

- [x] 3.1 `cmd/pmox/ssh_test.go`: add tests for `runShell` with zero args under the three picker modes (one-VM auto-select, multi-TTY picker, non-TTY error). Stub `vm.Pick` via the existing test-override pattern.
- [x] 3.2 `cmd/pmox/delete_test.go`: add tests that the picker runs before the confirmation prompt and that `--yes` with no positional + single VM auto-deletes after the picker auto-selects.
- [x] 3.3 `cmd/pmox/start_test.go`, `cmd/pmox/stop_test.go`, `cmd/pmox/info_test.go`: add one no-arg test per command covering the one-VM auto-select path (the multi-VM picker path is already covered by `vm.Pick` tests).
- [x] 3.4 Confirm no existing tests break: explicit-target invocations still route through `vm.Resolve` and never call `vm.Pick`. (`go test ./...` green.)

## 4. Specs + docs

- [ ] 4.1 Archive via `/opsx:archive` after implementation: update `openspec/specs/ssh-shell-exec/spec.md` and `openspec/specs/delete-command/spec.md` from the delta specs; add the new `openspec/specs/interactive-target-picker/spec.md`.
- [x] 4.2 Update `README.md` usage snippets for `shell`, `exec`, `delete`, `start`, `stop`, `info` to show the bare-command form and mention the picker.
- [x] 4.3 Update `openspec/llms.txt` (if it inventories commands) to note the optional positional. (No-op: `openspec/llms.txt` does not yet exist; it will be created by the in-flight `docs-and-llms-txt` change, which can note the optional positional at that time.)

## 5. Manual validation

- [x] 5.1 With one pmox VM on the cluster: verified `pmox exec -- whoami`, `pmox info`, `pmox list` all auto-select the single smoke VM. `pmox info smoke` still works (explicit arg regression).
- [ ] 5.2 With two or more pmox VMs: run `pmox shell`, confirm the picker renders with the list columns. Arrow-select, confirm the right VM connects.
- [ ] 5.3 With zero pmox VMs: run `pmox shell`, confirm the "no pmox VMs found — run `pmox launch`" error.
- [ ] 5.4 Pipe the output: `pmox shell | cat`, confirm the command errors identically to today (no picker).
- [ ] 5.5 Regression: `pmox shell smoke` still works exactly as before when an explicit argument is passed.
