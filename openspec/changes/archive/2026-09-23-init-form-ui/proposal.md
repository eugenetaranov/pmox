## Why

`pmox init` is a long chain of one-at-a-time prompts: you answer, it moves
on, and there's no way to see what's coming or fix an earlier answer
without aborting and starting over. A form-style flow — visible fields you
can tab through, and a review screen before anything is written — is far
friendlier for first-time setup.

A pure single-form "answer everything then submit" can't work here: the
node/template/storage/bridge choices are populated by API calls that need
the URL and token first, and credential/SSH validation happens mid-flow.
So the redesign groups init into a few navigable form pages with a final
review, respecting those dependencies.

## What Changes

- **Phased form UI** using `huh` forms, each page navigable (tab/shift-tab)
  and editable before it submits:
  - **Connection** — API URL + token source (generate: `user@realm` /
    password / token name; or paste: token id / secret). URL reachability
    and credential validity are checked as the page submits; failures keep
    you on the page to fix them.
  - **Defaults** — node, template, storage, snippet-storage, bridge as
    select fields pre-filled from discovery; single-option fields still
    auto-fill. Editable together, not one-by-one.
  - **Access** — SSH key (generate / pick / browse), default user, and node
    SSH credentials.
- **Final review screen** listing every chosen value, with the ability to
  jump back to a page and change answers before **Confirm** writes the
  config. Nothing is persisted until Confirm.
- **Logic/UI split for testability**: data collection (the forms) sits
  behind seams that return typed answer structs; the probe / auth /
  discovery / validation / write logic stays plain functions and keeps its
  unit tests. Existing granular behavior (URL canonicalization, probe
  classification, token creation, key generation) is unchanged.
- **Non-interactive / `--no-input`**: forms require a TTY; when input is
  non-interactive the existing text-prompt fallbacks are used (or it errors
  with guidance), so scripts and CI are unaffected.
- The `--list`, `--remove`, `--regen-cloud-init` subflows are unchanged.

## Capabilities

### New Capabilities

- `init-form-ui` — the phased-form `pmox init` experience: the Connection /
  Defaults / Access pages, in-page navigation and validation, and the
  final editable review-before-write.

## Impact

- `cmd/pmox/init.go` — restructure `runInteractive` into
  collect-connection → probe/auth → discover → collect-defaults →
  collect-access → review → write; add answer structs and form builders
  behind seams. Reuse existing probe/`validateCredentials`/discovery/
  `generateToken`/`promptSSHKey`/`promptNodeSSH` logic.
- `internal/tui` — small helpers for building/running `huh` forms if
  needed.
- Tests: orchestration with stubbed form seams (happy path, back-and-edit,
  re-discovery after a connection edit, non-interactive fallback).
- Docs: README + llms.txt (describe the form flow + review screen).
