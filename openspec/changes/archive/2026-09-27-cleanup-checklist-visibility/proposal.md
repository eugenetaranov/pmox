## Why

`pmox cleanup`'s interactive checklist is supposed to always list every
category it checks — including empty ones, dimmed — so it stays a
complete map of what cleanup covers (`feat(cleanup): list every category
in the checklist, not just non-empty ones`, commit 3fc2b53). That
requirement was never captured in a live spec: the only spec text for
`cleanup` at all lives in an archived change
(`2026-09-23-cleanup-selectable`) and still describes the old, wrong
behavior ("present a multi-select checklist of the categories that have
items"). With no live spec, the correct behavior has no enforcement
point, and a user just hit a real-world symptom of that gap: the
checklist appeared to be missing most categories (templates, SSH keys,
etc.), which looked exactly like the old bug had come back.

Investigating that report found the categories were not actually
missing from the underlying data — they were scrolled out of view. The
`huh` MultiSelect widget positions its initial viewport at the first
pre-checked option. Categories are listed in a fixed order; when the
only non-destructive category with items to clean happens to sit late
in that order (as it did here), the viewport jumps straight to it,
pushing every earlier category above the visible window with no
indication there's more to scroll to. This is functionally
indistinguishable from the original bug the user was worried about, so
it needs the same durable fix: a live, mandatory spec that pins down not
just "every category is listed" but "a populated category is never
hidden by the initial scroll position" — plus the code fix and a
regression test, so this can't quietly regress again.

## What Changes

- Establish a live `cleanup` spec capability (none exists today; specs
  for local/remote category collection, template opt-in, and selection
  flags all currently live only inside the archived
  `2026-09-23-cleanup-selectable` change and are not enforced).
- Supersede the stale "checklist of the categories that have items"
  requirement with the actual, correct behavior: every category in
  `cleanupCategories` is always listed; empty ones are shown dimmed
  ("nothing to clean") rather than absent.
- Add a new requirement closing the scroll-visibility gap: options
  passed to the interactive checklist are ordered with populated
  categories first (in their existing relative order), empty ones
  after — so the widget's initial-scroll-to-first-checked behavior can
  never push a populated category out of the initial viewport,
  regardless of which category happens to have items in a given run.
- Fix `cmd/pmox/cleanup.go`'s option-building to sort by "has items"
  before rendering the checklist, and add a regression test asserting
  populated categories precede empty ones in the option list actually
  passed to the picker (via the existing `selectCategoriesFn` seam),
  independent of `cleanupCategories`' declared order.

## Capabilities

### New Capabilities
- `cleanup`: `pmox cleanup`'s category model, collection, interactive
  checklist (completeness and ordering), non-interactive selection
  flags, and dry-run/apply semantics.

### Modified Capabilities
(none — `cleanup` has no live spec today; this introduces it fresh,
superseding the stale archived-change text rather than editing a live
delta)

## Impact

- `cmd/pmox/cleanup.go`: option-ordering fix in the interactive
  checklist construction.
- `cmd/pmox/cleanup_selectable_test.go`: new regression test for
  populated-before-empty option ordering.
- `openspec/specs/cleanup/spec.md`: new live spec (this change's own
  artifact), the durable enforcement point going forward.
