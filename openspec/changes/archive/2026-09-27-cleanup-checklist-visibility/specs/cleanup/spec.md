## ADDED Requirements

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
of the checklist widget's own initial-scroll behavior (which may
position the viewport at the first pre-checked option) and regardless
of which specific category or categories happen to have items in a
given run. This SHALL be achieved by ordering the options passed to the
checklist widget with every populated category (with items, whether
pre-checked or not) before every empty category, rather than relying on
the categories' otherwise-meaningful declared order.

#### Scenario: A late-ordered populated category is not scrolled past

- **WHEN** the only non-destructive category with items to clean is one
  that is declared late in the fixed category list, and earlier
  categories in that list currently have no items
- **THEN** the checklist's option order still places every populated
  category (including any destructive ones with items) before the
  empty ones, so the checklist widget's initial viewport shows the
  populated categories rather than scrolling past them

#### Scenario: Multiple populated categories across the declared order

- **WHEN** categories with items to clean are scattered non-contiguously
  through the declared category list (some early, some late), mixed
  with empty categories
- **THEN** the checklist option order groups all populated categories
  together ahead of all empty categories, preserving each group's
  relative order
