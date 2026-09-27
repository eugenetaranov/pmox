## Purpose

`pmox cleanup` reclaims removable pmox leftovers on the local machine
and the configured Proxmox cluster(s) — orphaned snippets, abandoned
VMs, dead state, stale local records — grouped into named categories,
dry-run by default. On a terminal it shows a complete, always-visible
checklist of every category it checks, so an operator never has to
wonder whether a category with nothing to show is missing or simply
empty, and never has a populated category hidden from view by the
checklist widget's own scrolling behavior.

## Requirements

### Requirement: Cleanup categories

`pmox cleanup` SHALL collect removable pmox leftovers grouped into
named categories (declared in a single fixed list, e.g. orphaned
snippets, abandoned VMs, dead mount records, orphaned logs, orphaned
cloud-init files, stale tack profiles, stale VM identity records,
orphaned secrets, stale known_hosts pins, the orphaned pmox SSH
bootstrap key, orphaned pmox API tokens, configured server contexts,
and the local tack config directory). It SHALL remain dry-run by
default and remove only under `--apply` or after an explicit
"Remove N item(s) now? [y/N]" confirmation.

#### Scenario: Dry-run reports without removing

- **WHEN** `pmox cleanup` runs without `--apply` and confirmation is
  declined
- **THEN** it lists the items it would remove, grouped by category, and
  removes nothing

#### Scenario: Apply removes selected items

- **WHEN** `pmox cleanup --apply` runs
- **THEN** the selected items are removed and a summary is printed

### Requirement: Interactive checklist always lists every category

`pmox cleanup` SHALL, on a terminal and when no selection flags are
given, present a multi-select checklist listing every declared
category, not only the ones that currently have items. A category with
at least one item SHALL show its item count and, unless it is a
destructive category, start pre-checked. A category with no items
found SHALL still appear in the list — dimmed, annotated as having
nothing to clean — rather than being omitted.

#### Scenario: Populated category shows its count and is pre-checked

- **WHEN** a non-destructive category has one or more items to clean
- **THEN** the checklist shows that category with its item count and it
  starts checked

#### Scenario: Empty category is listed, not omitted

- **WHEN** a category currently has no items to clean
- **THEN** the checklist still shows that category, dimmed, annotated as
  having nothing to clean, and it starts unchecked

#### Scenario: Destructive category with items starts unchecked

- **WHEN** a destructive category (e.g. templates, server contexts, the
  tack config directory) has one or more items to clean
- **THEN** the checklist shows its item count but it starts unchecked

### Requirement: A populated category is never hidden by the initial scroll position

The interactive checklist SHALL be built so that no category with items
to clean is scrolled out of the initially visible viewport, regardless
of the checklist widget's own initial-scroll behavior (which positions
the viewport at the index of the first pre-checked option, if any) and
regardless of which specific category or categories happen to have
items in a given run. This SHALL be achieved by ordering the options
passed to the checklist widget in three tiers: every checked category
first, then every unchecked-but-populated category, then every empty
category — each tier preserving the categories' relative declared
order — rather than merely grouping populated categories ahead of empty
ones, since a populated-but-unchecked (destructive) category declared
earlier than the first checked category would otherwise still precede
the widget's scroll target and be scrolled past.

#### Scenario: A late-ordered checked category is not scrolled past

- **WHEN** the only non-destructive category with items to clean is one
  that is declared late in the fixed category list, and earlier
  categories in that list currently have no items
- **THEN** the checklist's option order still places that checked
  category at the front, so the checklist widget's initial viewport
  shows it rather than scrolling past it

#### Scenario: An earlier-declared populated destructive category is not scrolled past

- **WHEN** a destructive category (never pre-checked) with items to
  clean is declared earlier in the fixed category list than the only
  checked category
- **THEN** the checklist option order still places the checked category
  at index 0, ahead of the earlier-declared populated destructive
  category, so neither is scrolled out of the initial viewport

#### Scenario: Multiple populated categories across the declared order

- **WHEN** categories with items to clean are scattered non-contiguously
  through the declared category list (some early, some late), mixed
  with empty categories
- **THEN** the checklist option order groups checked categories first,
  then unchecked-but-populated categories, then empty categories,
  preserving each group's relative order
