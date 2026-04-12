## 1. Confirmation helper in `internal/tui`

- [x] 1.1 Add `internal/tui/confirm.go` defining the `Confirmer` interface (`Confirm(ctx, prompt) (bool, error)`).
- [x] 1.2 Implement `NewTTYConfirmer(in io.Reader, out io.Writer)` returning a `Confirmer` that prints the prompt, reads one line, and returns true only on `y` / `yes` (case-insensitive, trimmed).
- [x] 1.3 Implement `AlwaysConfirmer` whose `Confirm` returns `(true, nil)` unconditionally and never touches I/O.
- [x] 1.4 Add a `StdinIsTerminal()` (or equivalently named) helper using `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))` so commands can detect non-interactive stdin without re-importing `x/term`.
- [x] 1.5 Add `internal/tui/confirm_test.go` with table-driven coverage for: `y`, `yes`, `Y`, `YES`, `n`, empty line, `maybe`, multi-line input, and a reader that errors mid-read.
- [x] 1.6 Add a unit test for `AlwaysConfirmer` that asserts no read/write occurs (use a reader/writer that fails on use).

## 2. Wire `pmox delete` to the helper

- [x] 2.1 Add a `--yes` / `-y` bool flag to `deleteFlags` in `cmd/pmox/delete.go`.
- [x] 2.2 Resolve `PMOX_ASSUME_YES` via the existing `envBool` helper at command-run time and OR it with the flag value.
- [x] 2.3 Add a `confirmer tui.Confirmer` field to `executeDelete`'s call signature (or a struct it accepts) so tests can inject a fake — match the existing fake-client injection pattern.
- [x] 2.4 In `runDelete`, build the production `Confirmer`: if assume-yes is set, use `AlwaysConfirmer`; otherwise, if stdin is a TTY, build a `TTYConfirmer` over `os.Stdin` + `cmd.ErrOrStderr()`; otherwise return the non-TTY refusal error specified in the spec.
- [x] 2.5 In `executeDelete`, after the tag check and before `GetStatus`, format the summary line (`About to delete VM %q (vmid %d, node %s, tags %s)`) and call the confirmer; on `false` exit non-zero with `delete cancelled`.
- [x] 2.6 Extend the prompt text when `--force` is in effect to mention hard-stop and tag-bypass per spec scenario "`--force` prompt warns about hard stop and tag bypass".
- [x] 2.7 Update `cmd/pmox/main.go` (or wherever delete is registered) to thread the new flag and make sure the cobra long-help text mentions `--yes` / `PMOX_ASSUME_YES`.

## 3. Tests for the gated delete flow

- [x] 3.1 In `cmd/pmox/delete_test.go`, add a fake `Confirmer` that records the prompt it received and returns a configurable bool/err.
- [x] 3.2 Add test "denies → no destructive call": fake confirmer returns false; assert `shutdownHits + stopHits + deleteHits == 0` and exit error mentions "cancelled".
- [x] 3.3 Add test "approves → existing flow runs": fake confirmer returns true; assert the existing happy-path counters fire (shutdown → delete).
- [x] 3.4 Add test "`--yes` skips prompt": pass `--yes`, use a fake confirmer that fails on call; assert it was never called and the destroy ran.
- [x] 3.5 Add test "`PMOX_ASSUME_YES=1` skips prompt": same as 3.4 but via env var.
- [x] 3.6 Add test "non-TTY without bypass → refusal": stub the TTY detector seam to report non-TTY, assert error mentions `--yes` and `PMOX_ASSUME_YES`, assert zero destructive calls.
- [x] 3.7 Add test "tag check fails → no prompt": run against an untagged VM without `--force`, assert error is the existing tag error AND that the confirmer was never called.
- [x] 3.8 Add test "`--force` still prompts": untagged VM with `--force` and a fake confirmer returning false; assert no destructive calls, assert prompt summary string contained `FORCE` (or whatever the design's wording is).
- [x] 3.9 Add test "summary contains name, vmid, node, tags": capture the prompt the confirmer received and assert each field is present.
- [x] 3.10 Add test "already-gone VM short-circuits before prompt": fake `GetStatus` 404; assert exit 0 and the confirmer was never called (resolution that the VM is gone means there's nothing to confirm).

## 4. Documentation + commit

- [x] 4.1 Update README's `pmox delete` section: add a "Confirmation" subsection covering the prompt, `--yes` / `-y`, `PMOX_ASSUME_YES`, and the non-TTY behavior. Call out the migration note for scripted callers.
- [x] 4.2 Run `go build ./...` and `go test ./...` and confirm green.
- [x] 4.3 Run `golangci-lint run --timeout=3m` and confirm clean.
- [x] 4.4 Commit with a message that explicitly tells script authors to add `--yes` or set `PMOX_ASSUME_YES=1`.
- [x] 4.5 Mark all tasks above as done and run `openspec status --change confirm-destructive-commands` to confirm the change is implementation-complete.
