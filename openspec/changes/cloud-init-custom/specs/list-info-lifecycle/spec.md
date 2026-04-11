## ADDED Requirements

### Requirement: Snippet cleanup on delete

`pmox delete` SHALL remove any pmox-owned snippet referenced by
the deleted VM's `cicustom` config value. Cleanup runs after the
destroy task completes and is best-effort.

#### Scenario: Custom cloud-init VM cleanup
- **WHEN** `pmox delete web1` is invoked and the VM has `cicustom=user=local:snippets/pmox-104-user-data.yaml`
- **THEN** the delete command SHALL call `DeleteSnippet(node, "local", "pmox-104-user-data.yaml")` after the destroy task completes

#### Scenario: Built-in cloud-init VM has nothing to clean up
- **WHEN** `pmox delete web1` is invoked and the VM config has no `cicustom` key
- **THEN** the delete command SHALL NOT call `DeleteSnippet`

#### Scenario: Failed cleanup warns but does not fail delete
- **WHEN** `DeleteSnippet` returns an error that is not `ErrNotFound`
- **THEN** the delete command SHALL print a warning to stderr
- **AND** SHALL exit 0

#### Scenario: cicustom parse failure warns and skips cleanup
- **WHEN** the VM's `cicustom` value is malformed or does not start with `user=`
- **THEN** the delete command SHALL print a warning to stderr
- **AND** SHALL NOT call `DeleteSnippet`
- **AND** SHALL exit 0
