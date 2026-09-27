## Context

`huh.MultiSelect.selectOptions()` (vendored, `field_multiselect.go`) sets
the initial viewport scroll position to the index of the first option
in the slice with `.selected == true` — i.e. the first *checked*
option, not the first populated one. The prior fix
(`2026-09-27-cleanup-checklist-visibility`) sorted populated categories
before empty ones, which is necessary but not sufficient: within the
populated group, a destructive (never pre-checked) category declared
earlier than the first non-destructive checked category still preceded
the widget's jump target and got scrolled out of the initial viewport.

Verified live via a pty-driven capture of the real interactive checklist
(both before and after this fix) against real cluster state with two
genuine pmox templates present: before the fix, the "pmox templates
(DESTRUCTIVE) (2)" row was entirely absent from the rendered output
despite `counts["template"] == 2`; after, it renders as the second row
(right after the one checked category), with the checked category
correctly at index 0.

## Goals / Non-Goals

**Goals:** the option ordering must guarantee that if any category is
checked, it is at index 0 — so `selectOptions()`'s scroll-to-first-
checked jump can never land past a populated-but-unchecked category.

**Non-Goals:** no change to which categories are checked by default
(unchanged: non-destructive + populated); no further `huh` behavior
changes.

## Decisions

**Decision: three-tier sort — checked, then populated-unchecked, then
empty — rather than two-tier (populated, then empty).**

Since every checked category is, by construction, always populated
(`sel[key]` is only ever set for non-destructive categories that are in
`avail`), "checked" is strictly a subset of "populated." Splitting the
populated group into "checked" and "unchecked" and placing checked
first guarantees the scroll target (if `selectOptions()` finds one) is
always at absolute index 0, regardless of how many destructive
populated categories exist or where they're declared.

## Risks / Trade-offs

- [Risk] If, in some future scenario, *multiple* categories end up
  checked simultaneously in the interactive path, only the first is
  guaranteed at index 0 — the rest would sort together right after it,
  which is fine (all still land in the always-checked, front-most tier,
  never behind an empty or unchecked-destructive row).
