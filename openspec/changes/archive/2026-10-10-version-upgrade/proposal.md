## Why

pmox can't update itself. Users have to remember how they installed it
(Homebrew, or a binary copied from a release) and upgrade it by hand.

## What Changes

- New `pmox version upgrade [--check] [--yes] [--version vX.Y.Z]`.
- It finds the latest GitHub release, compares it with the running
  version, and works out how pmox was installed:
  - **Homebrew:** it runs `brew upgrade eugenetaranov/tap/pmox` and
    exits with brew's status.
  - **Plain binary:** it downloads the release archive, verifies its
    sha256, checks the new binary runs, and atomically replaces itself.
    When the binary's directory isn't writable, it uses `sudo install`,
    which prompts for the password.
- It asks first ("Upgrade pmox 0.34.1 → 0.35.0 via Homebrew?", default
  Yes). `--yes` skips the question, and `--check` only reports.
- `pmox version` with no verb prints the version, unchanged.

## Capabilities

### New Capabilities
- `self-upgrade`: finding the latest release, detecting the install
  method, confirmation, the Homebrew route, and the binary route.

### Modified Capabilities
- `cli-command-tree`: `version` gains one verb, `upgrade`.

## Impact

- New `cmd/pmox/upgrade.go`. `cmd/pmox/main.go` (`newVersionCmd`).
- README install section and llms.txt.
- Needs network access to api.github.com and github.com release
  downloads. No new dependencies.
