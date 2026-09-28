## ADDED Requirements

### Requirement: The interactive checklist is preceded by a full item-detail preview

`pmox cleanup` SHALL, immediately before presenting the interactive
checklist, print the full detail of every found item — the same
per-item text the post-selection report uses — grouped by category,
for every category that currently has at least one item, regardless of
whether that category is checked by default. A category with nothing
to clean SHALL NOT appear in this preview. This preview SHALL be purely
informational: it SHALL NOT affect which items are actually removed,
which remains governed entirely by the checklist's final selection.

#### Scenario: A destructive category's items are identifiable before checking it

- **WHEN** a destructive category (starts unchecked) has one or more
  items to clean
- **THEN** the preview printed before the checklist includes that
  category's full item detail (e.g. name, vmid, node), not just its
  count

#### Scenario: No preview when nothing was found

- **WHEN** no category currently has any items to clean
- **THEN** no preview is printed, and the checklist is shown directly

#### Scenario: Empty categories are excluded from the preview

- **WHEN** some categories have items and others do not
- **THEN** the preview lists only the categories with items; the
  checklist still lists every category, including the empty ones
