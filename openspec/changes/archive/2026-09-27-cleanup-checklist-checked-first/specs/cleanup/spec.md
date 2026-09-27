## MODIFIED Requirements

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
