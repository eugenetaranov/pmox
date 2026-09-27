## Why

The previous fix for `cleanup`'s checklist visibility (`2026-09-27-cleanup-checklist-visibility`) ordered options with all populated categories before all empty ones, on the theory that `huh`'s MultiSelect scrolls its initial viewport to the first *populated* option. That theory was incomplete: `huh` actually scrolls to the first *checked* (`.Selected(true)`) option specifically. A populated-but-unchecked destructive category (e.g. `template`) declared earlier than the first checked category (e.g. `api-token`) still got scrolled past — reproduced live: two real pmox templates existed and were correctly counted, yet the "pmox templates (DESTRUCTIVE)" row was entirely absent from the rendered checklist, exactly the same symptom the prior fix was meant to eliminate.

## What Changes

- Correct the `cleanup` spec's "populated category is never hidden" requirement: the checklist SHALL order options as checked-first, then populated-but-unchecked, then empty — not merely populated-before-empty — so the widget's scroll-to-first-checked jump target is always at index 0 when any category is checked.
- `cmd/pmox/cleanup.go`'s `resolveSelection` already implements this (three-tier sort), verified live via a pty-driven reproduction of the real bug before and after the fix.

## Capabilities

### Modified Capabilities
- `cleanup`: replace the "A populated category is never hidden by the initial scroll position" requirement with the corrected checked-first ordering rule.

## Impact

- `openspec/specs/cleanup/spec.md`: requirement text corrected.
- `cmd/pmox/cleanup.go` / `cmd/pmox/cleanup_selectable_test.go`: already fixed and tested in this same session; this change lands the spec correction to match.
