## Context

`internal/pveclient.Client.Clone` today hard-codes `form.Set("full", "1")` and `internal/launch` relies on the resulting VM being disk-independent from its template (it calls `Resize`, rewrites cloud-init, and lets `pmox delete` destroy the VM without fear of taking the template with it). The existing `pveclient-core` spec mentions `full=1` only inside a single happy-path scenario, which is too weak — a future refactor could reasonably add a `full bool` parameter or a `CloneLinked` variant without violating any stated requirement. This change locks the invariant in at the spec level.

## Goals / Non-Goals

**Goals**
- Make "always full clone" a stated requirement of `pveclient-core`, not an implementation accident.
- Forbid a caller-controlled `full` knob and forbid a linked-clone code path in the client.

**Non-Goals**
- No new CLI surface. No new config. No new capabilities.
- No change to `launch` behavior; this is a spec-tightening change only.
- Not evaluating whether linked clones would ever be useful (they would not, for pmox's ephemeral-VM model).

## Decisions

### Decision: Forbid the `full` knob at the client API level, not via runtime check
- **What**: The `Clone` function signature must not grow a `full` parameter; the form value is hard-coded inside the function.
- **Why**: A runtime check ("reject full=0") would be dead code in a single-binary tool. The simpler guarantee is "there is no way to ask for a linked clone," enforced by API shape. This is also what the code already does — the spec just needs to catch up.
- **Alternatives considered**:
  - *Add a `full bool` parameter defaulting to true*: rejected. Opens a door that has no use case and invites future regression.
  - *Add a runtime assertion*: rejected. Nothing to assert against; the value is a constant.

### Decision: Model as MODIFIED Requirement, not ADDED
- **What**: The existing "Clone endpoint" requirement in `pveclient-core/spec.md` is replaced with a tightened version, not supplemented with a new requirement.
- **Why**: The new constraint is a property of the same behavior. Keeping it as one requirement avoids fragmenting the Clone contract across two places. Per the OpenSpec instruction, MODIFIED must include the ENTIRE updated requirement block, which this change does.

### Decision: No code change required
- **What**: `internal/pveclient/vm.go` already sets `full=1` unconditionally and the existing test already asserts it. This change adds no new test.
- **Why**: The intent of this change is spec hardening. Adding a redundant test would duplicate `TestClone_HappyPath` without covering a new branch. If a future edit ever introduces a `full` parameter, the spec (and code review against the spec) is the gate.

## Risks / Trade-offs

- **Risk**: A future contributor reads the tightened spec as "we considered linked clones and rejected them" and re-opens the discussion. Mitigated by the Why in the proposal naming the concrete failure mode (template coupling).
- **Trade-off**: We are closing a door we never opened. The cost is near zero; the benefit is preventing silent regression during future refactors of `pveclient`.

## Migration Plan

None. No behavior changes, no data migration, no user-visible surface change. Archiving this change after merge updates `openspec/specs/pveclient-core/spec.md` in place.

## Open Questions

None.
