## Context

`internal/credstore` is a thin wrapper over `github.com/zalando/go-keyring`
that persists pmox secrets (API token, node SSH password, key passphrase)
keyed by canonical server URL. Its package-level functions — `Get`, `Set`,
`Remove`, and the node-SSH helpers (`GetNodeSSHPassword`,
`SetNodeSSHKeyPassphrase`, …) — are called by `cmd/pmox/configure.go` and
`internal/server/resolver.go` (hydrate). Today every call goes straight to
the OS keychain; on a host with no Secret Service / keychain daemon those
calls error, which cascades into `configure`, server resolution, and all
launch/lifecycle commands.

We want keychain-first behavior preserved for the 99% case and a graceful,
automatic fallback for headless/CI hosts, without changing the stable
`credstore` API or the `config.yaml` shape.

## Goals / Non-Goals

**Goals:**
- Keep the existing `credstore` package API stable so no caller changes.
- Use the keychain when present; fall back to a `0600` `secrets.yaml`
  automatically when it isn't.
- Let power users force a backend via `PMOX_SECRET_STORE`.
- Reads tolerant across backends; removal clears all backends.
- Surface the active backend (and a plaintext-on-disk warning) in `doctor`.

**Non-Goals:**
- Encrypting the fallback file (age/gpg) — future enhancement; v1 relies on
  file permissions only.
- Automatic bulk migration of secrets between backends beyond tolerant
  reads (secrets naturally re-home on the next write).
- Any change to `config.yaml` or to how secrets are keyed.

## Decisions

**1. Backend interface behind the existing package API.**
Introduce an unexported `backend` interface (`get/set/remove(account) `) with
two implementations: `keychainBackend` (wraps go-keyring) and `fileBackend`
(new). The package-level `Get/Set/Remove` become thin dispatchers over a
resolved backend. Rationale: preserves the caller contract (`credstore.Get`
etc. unchanged), keeps the change contained to one package, and makes each
backend independently testable. *Alternative considered:* exposing a
`Store` struct callers construct — rejected because it forces churn across
configure/resolver for no benefit.

**2. Selection = `PMOX_SECRET_STORE` × availability, resolved once.**
`auto` (default) picks keychain-if-available-else-file; `keychain` forces
keychain (error if absent); `file` forces file. Availability is probed once
per process with a sentinel `set→get→delete` on a reserved account and the
result cached in a `sync.Once`. Rationale: a per-call probe is wasteful and
some keyring backends are slow; one probe is enough. *Alternative:* trust an
error from the first real call — rejected because a real Get "not found" is
indistinguishable from "no keychain," and we don't want to mis-route writes.

**3. `secrets.yaml` layout mirrors the keychain account scheme.**
`map[canonicalURL]map[secretKind]value`, where `secretKind` is `token`,
`node_ssh_password`, `node_ssh_key_passphrase`. The file backend maps the
existing account string (URL + suffix) onto this two-level map. Rationale:
one file, human-inspectable, same keying as the keychain so tolerant reads
and dual removal are trivial. Written atomically (temp + `Rename`), `0600`
in the `0700` config dir — identical discipline to `config.go`'s `Save`.

**4. Tolerant reads, keychain-preferred writes (auto only).**
`auto` Get: try keychain, then file. `auto` Set: keychain if available else
file. `keychain`/`file` modes are single-backend. Rationale: a machine that
gains or loses a keychain between runs still finds prior secrets; writes
prefer the more secure store when it exists.

**5. Removal fans out to every backend unconditionally.**
`Remove` deletes from both keychain and file, swallowing not-found. Rationale:
guarantees no stray secret after `delete-context`/`--remove`, regardless of
where it was written or whether the environment changed.

**6. `doctor` gains one check.** A `secrets.backend` check reports keychain vs
file and warns on file (plaintext on disk). Reuses the resolver's cached
availability probe, so it's cheap and read-only.

## Risks / Trade-offs

- **Plaintext secrets on disk in fallback mode** → Mitigated by `0600`/`0700`
  perms, never writing to `config.yaml`, a loud `doctor` warning, and docs
  stating it's a deliberate headless tradeoff. Encryption is a noted future
  step.
- **Keychain probe writes a sentinel entry** → Use a clearly-named reserved
  account and delete it immediately; tolerate probe failure as "unavailable."
- **Availability changes mid-life leave a secret in the "wrong" backend** →
  Tolerant reads find it either way; the next write re-homes it; removal
  clears both. No silent loss.
- **A future keyring quirk where set succeeds but get fails** → The sentinel
  probe does set→get→delete, so such a backend is correctly treated as
  unavailable rather than silently losing writes.
- **Concurrent pmox processes writing `secrets.yaml`** → Atomic temp+rename
  makes each write all-or-nothing; last-writer-wins is acceptable for this
  low-frequency, single-user file (same assumption as `config.yaml`).

## Migration Plan

Additive and backward-compatible: existing users have a keychain, so `auto`
detects it and behaves exactly as today — no migration needed. New
headless users get the file backend automatically. No rollback concerns; the
change can be reverted without data changes (keychain entries are untouched;
a `secrets.yaml`, if created, is simply left in place).

## Open Questions

- Should `keychain`-mode failures be a distinct exit code, or reuse
  `ExitGeneric`/`ExitUserError`? (Leaning `ExitUserError` — it's a
  configuration/environment problem the user can fix.)
- Do we want a `pmox config migrate-secrets` helper later to force-move
  secrets between backends? (Out of scope now; tolerant reads cover the need.)
