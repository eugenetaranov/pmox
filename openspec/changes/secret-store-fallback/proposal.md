## Why

pmox stores every secret (API token, node SSH password, key passphrase) only
in the OS keychain via `internal/credstore`. When no keychain is available —
headless Linux with no Secret Service / D-Bus, CI runners, containers — every
keychain call fails hard, so `configure`, server resolution, and all
launch/lifecycle commands break and pmox is unusable there. It should keep
using the keychain when present, and fall back gracefully when it isn't.

## What Changes

- Introduce a **secret-store backend abstraction** behind the existing
  `credstore` package API: a keychain backend (today's go-keyring) and a new
  **file backend**, with a resolver that picks between them.
- **Automatic detection**: probe keychain availability once per process; use
  the keychain when present, otherwise the file backend. No flag needed for
  the common case.
- **File fallback**: secrets are written to a separate
  `<config-dir>/pmox/secrets.yaml` (mode `0600` inside the `0700` config dir),
  written atomically, keyed by canonical server URL with the same logical
  secrets. Secrets are **never** written into `config.yaml`.
- **Override**: `PMOX_SECRET_STORE=auto|keychain|file` (default `auto`) forces
  a backend — `keychain` errors if unavailable; `file` always uses the file.
- **Tolerant reads**: under `auto`, look up the keychain first, then the file,
  so secrets stored under one backend are still found if the environment later
  gains/loses a keychain. Writes under `auto` go to the keychain when present,
  else the file.
- **Removal clears both backends** so `pmox config delete-context` /
  `configure --remove` never leave a stray secret behind.
- **`pmox doctor`** reports which backend is active and warns when the file
  fallback is in use (plaintext on disk, protected only by file permissions).

Non-breaking: `config.yaml` shape is unchanged, and existing keychain users
see no behavior change (auto detects the keychain and uses it).

## Capabilities

### New Capabilities
- `secret-store`: backend-agnostic persistence of pmox secrets — keychain-first
  with an automatic, permission-restricted file fallback, an override, tolerant
  reads across backends, and removal that clears all backends.

### Modified Capabilities
- `configure-and-credstore`: secret persistence and removal are redefined in
  terms of the new secret-store (keychain-or-file) rather than keychain-only;
  `configure`/`--remove` and context deletion clear secrets from every backend.

## Impact

- `internal/credstore`: refactor into keychain + file backends + a resolver;
  keep the package-level `Get`/`Set`/`Remove` and node-SSH helpers as the
  stable API so `cmd/pmox/configure.go` and `internal/server/resolver.go`
  (hydrate) need no changes.
- New `secrets.yaml` under the pmox config dir (XDG-aware); `.gitignore`
  already excludes the config dir on user machines, but the file must be 0600.
- `cmd/pmox/doctor.go`: add a secret-backend check.
- New env var `PMOX_SECRET_STORE`; documented in README/llms.txt.
- Dependency: continues to use `github.com/zalando/go-keyring`; adds no new deps
  (file backend uses `gopkg.in/yaml.v3`, already vendored).
