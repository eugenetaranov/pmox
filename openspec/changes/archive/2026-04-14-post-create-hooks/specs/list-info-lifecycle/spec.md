## ADDED Requirements

### Requirement: Clone accepts hook flags

The `pmox clone` command SHALL accept `--post-create`, `--tack`, `--ansible`, and `--strict-hooks` with identical semantics to `pmox launch`.

#### Scenario: Clone runs post-create hook after the clone succeeds
- **WHEN** `pmox clone --post-create ./p.sh src new` completes successfully
- **THEN** the post-create script SHALL run after the cloned VM's SSH wait phase
- **AND** `PMOX_IP` SHALL equal the cloned VM's discovered IP

#### Scenario: Clone enforces hook mutual exclusion
- **WHEN** `pmox clone --tack t.yaml --ansible a.yaml src new` is invoked
- **THEN** the command SHALL error before any PVE call
- **AND** the error SHALL contain `mutually exclusive`
