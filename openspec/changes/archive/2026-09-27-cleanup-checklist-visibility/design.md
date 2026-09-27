## Context

`pmox cleanup`'s interactive checklist is built in `cmd/pmox/resolveSelection`
(`cmd/pmox/cleanup.go`): it iterates the fixed, declared-order
`cleanupCategories` slice, builds one `huh.Option` per category via
`categoryLabel(c, counts[c.key])` (dimmed "nothing to clean" for
`counts[c.key] == 0`, `"<title> (<n>)"` otherwise), marks it `.Selected(true)`
when it's in the default-selected set (`sel[c.key]`), and hands the whole
option list to `tui.SelectMultiChecked` → `huh.NewMultiSelect`.

`huh.MultiSelect`'s `selectOptions()` (vendored, `field_multiselect.go`)
runs on init and sets `m.cursor` / `m.viewport.YOffset` to the index of the
**first** option marked `.Selected(true)` in the slice — a `bubbles/viewport`
scroll position, not a re-sort. With `defaultHeight = 10` and per-field
title/help chrome eating a few of those lines, the visible viewport is
smaller than the full category list (currently 14 entries), so this
YOffset jump can genuinely hide earlier rows below the fold with no
on-screen indication that anything is above the current view.

`cleanupCategories`' declared order is fixed and unrelated to which
categories happen to have items in a given run. When the only
non-destructive category with items sits late in that order (as `api-token`
does), the scroll lands there, and everything before it — populated or
not, destructive or not — is pushed out of the initial viewport.

## Goals / Non-Goals

**Goals:**
- Every category is always listed (already true today; this design
  doesn't change that part, just gives it a live spec).
- A category with items to clean is never hidden by the widget's initial
  scroll position, regardless of which category that happens to be.
- The fix is self-contained in `cmd/pmox/cleanup.go` and testable without
  a real TTY, via the existing `selectCategoriesFn` seam.

**Non-Goals:**
- Not attempting to change `huh`'s scroll/cursor behavior itself (it's a
  vendored third-party dependency; forking it is out of proportion to
  this bug).
- Not guaranteeing every category is visible without ANY scrolling in
  all terminal sizes — only that populated ones are never the ones
  pushed out of view. A very small terminal can still require scrolling
  to see trailing empty categories, which is fine: those are inert.

## Decisions

**Decision: sort populated categories before empty ones when building
the option list, rather than changing `cleanupCategories`' declared
order or touching `huh`.**

- `cleanupCategories`' order is otherwise meaningful (roughly: remote
  categories, then local ones, then the two destructive teardown
  categories last) and used elsewhere (e.g. `--only`/`--skip` validation
  messages, report ordering). Reordering the *declared* list to chase
  "whichever category has items this run" would be both fragile (order
  changes run-to-run) and would corrupt that otherwise-meaningful
  ordering for every other consumer.
- Instead, sort only the transient option slice built for the
  interactive checklist: categories with `counts[c.key] > 0` first (in
  their existing relative order — a stable sort), then categories with
  `counts[c.key] == 0` (also in existing relative order). This
  guarantees the first `.Selected(true)` option — whichever category
  that turns out to be — is always within the populated group, at or
  near the very front of the list, so `selectOptions()`'s YOffset jump
  never lands past a populated row.
- Alternatives considered and rejected:
  - **Increase `MultiSelect.Height()` to fit all categories.** Doesn't
    help: `viewport.YOffset` still starts at the first selected index;
    a taller viewport just means more empty space is rendered below a
    still-wrong scroll position, not that earlier rows become visible.
  - **Don't pre-mark any option `.Selected(true)`, apply the default
    selection some other way.** Would remove the pre-checked UX for
    non-destructive categories with items — a real behavior regression,
    not just a rendering fix.
  - **Patch/fork `huh`** to not auto-scroll on init. Correct in
    principle but disproportionate for one option list in one command;
    revisit only if this pattern recurs elsewhere.

## Risks / Trade-offs

- [Risk] Sorting changes the on-screen row order category-to-category
  between runs (populated categories "float up"), which could read as
  inconsistent to a user comparing two runs. → Mitigation: the order is
  still deterministic and stable within a single run, and the ordering
  is on the axis a user actually cares about here (what has something to
  clean vs. what doesn't) — an improvement over the current fixed but
  scroll-defeated order, not a regression.
- [Risk] This masks the underlying `huh` viewport quirk rather than
  fixing it, so any future checklist added elsewhere in pmox could hit
  the same bug independently. → Mitigation: documented here and in the
  new spec's requirement text so a future author searching for "wait_for
  checklist missing rows"-shaped bugs finds this precedent.
