## Why

The `Clone` call on `internal/pveclient` currently hard-codes `full=1`, and `launch` depends on the resulting VM being independent of its template (resize, cloud-init rewrite, destroy without affecting the template). Nothing in the spec forbids a future caller from adding a linked-clone path or a `full` parameter, and a regression here would silently couple every launched VM to its source template — breaking template upgrades and `pmox delete`. We want the "always full clone" invariant explicit in the spec so it cannot drift.

## What Changes

- Promote "full clone only" from an implementation detail to a stated requirement of `pveclient-core`.
- Forbid exposing `full` as a caller-controlled parameter on `Client.Clone`.
- Add a scenario locking the form body to `full=1` for all clone calls (not just the happy path).

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `pveclient-core`: the Clone endpoint requirement is tightened to mandate `full=1` unconditionally and to disallow a linked-clone code path.

## Impact

- Spec: `openspec/specs/pveclient-core/spec.md` — Clone requirement tightened.
- Code: `internal/pveclient/vm.go` — no behavior change; add a code comment referencing the requirement if helpful.
- Tests: `internal/pveclient/vm_test.go` — keep the existing `full=1` assertion; no new caller-facing surface.
- No user-visible CLI or config changes.
