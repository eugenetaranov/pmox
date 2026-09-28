## Why

The interactive checklist shows only a per-category count (e.g. "pmox
templates (DESTRUCTIVE) (2)"). A destructive category starts unchecked
by design, so an operator has no way to see *which* 2 templates that is
— name, vmid, node — without first ticking the box, which commits it to
the eventual removal just to look. A user asked for exactly this after
seeing "(2)" with no way to identify what it referred to.

## What Changes

- Before showing the interactive checklist, print every found item's
  full detail (the same per-item text the post-selection report already
  uses), grouped by category, for every category that currently has
  items — regardless of whether that category ends up checked. The
  checklist itself is unchanged (still shows counts, not inline
  detail); this is a preview printed just above it.
- No categories with nothing to clean are included in the preview
  (nothing to show); the checklist still lists them, unchanged.
- Purely informational: which items are actually removed is still
  governed entirely by checklist selection, unaffected by this preview.

## Capabilities

### Modified Capabilities
- `cleanup`: add a requirement that the interactive checklist is
  preceded by a full-detail preview of every found item.

## Impact

- `cmd/pmox/cleanup.go`: `resolveSelection` now takes the actual
  `[]cleanupItem` list (previously just a `counts map[string]int]`) and
  a writer, printing the preview via a new shared `printItemsByCategory`
  helper (extracted from `reportCleanup`, which uses the same helper for
  its own post-selection listing).
- `cmd/pmox/cleanup_selectable_test.go`: updated call sites, new tests
  for preview content and the "nothing found → no preview" case.
