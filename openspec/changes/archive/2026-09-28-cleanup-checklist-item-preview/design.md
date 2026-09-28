## Context

`resolveSelection` previously took `counts map[string]int` — enough to
render a per-category count in the checklist, but not enough to show
what any given item actually is. `reportCleanup` (the post-selection
listing) already had the full detail-printing logic, just scoped to
whatever categories ended up selected.

## Goals / Non-Goals

**Goals:** let an operator see exactly what a category refers to before
deciding whether to check it, especially destructive ones that start
unchecked.

**Non-Goals:** no change to what gets removed (still exactly the
checked categories); no change to the checklist's own row format
(still a count, not inline detail — keeps rows short).

## Decisions

**Decision: pass `resolveSelection` the actual `[]cleanupItem` slice
instead of a pre-computed `counts` map, and derive counts internally.**

This lets the same function print full detail via the shared
`printItemsByCategory` helper (extracted from `reportCleanup`, which
now calls the same helper for its own listing — one implementation of
"group by category, print title + count + indented detail lines", used
both before and after selection). Rejected keeping `counts` and adding
a separate `items` param alongside it: redundant, since `counts` is
trivially derived from `items`, and passing both invites them to drift
out of sync.

**Decision: no preview at all when `len(items) == 0`.** An empty
"Found items:\n\n" header immediately followed by the checklist would
be noise; the checklist's own per-category "— nothing to clean" rows
already communicate that state.

## Risks / Trade-offs

- [Risk] For a category with very many items (e.g. dozens of stale
  known_hosts pins), the preview could be long. → Not mitigated here:
  the same is already true of the post-selection report today, and
  cleanup categories are not expected to routinely reach a size where
  this matters; revisit if it becomes a real problem.
