## ADDED Requirements

### Requirement: Backend selection

pmox SHALL persist secrets through a backend selected at runtime. The
selection is controlled by `PMOX_SECRET_STORE` with values `auto`
(default), `keychain`, or `file`:

- `auto` — use the OS keychain when it is available, otherwise the file
  backend.
- `keychain` — use the OS keychain only; operations SHALL fail with a
  clear error when the keychain is unavailable.
- `file` — always use the file backend, even when a keychain is present.

Keychain availability SHALL be probed at most once per process (a
sentinel set/get/delete) and the result cached.

#### Scenario: auto uses the keychain when present
- **WHEN** `PMOX_SECRET_STORE` is unset (or `auto`) and the OS keychain is available
- **THEN** secrets SHALL be written to and read from the keychain

#### Scenario: auto falls back to the file when no keychain
- **WHEN** `PMOX_SECRET_STORE` is unset (or `auto`) and the OS keychain is unavailable
- **THEN** secrets SHALL be written to and read from the file backend
- **AND** no operation SHALL fail solely because the keychain is absent

#### Scenario: keychain mode errors without a keychain
- **WHEN** `PMOX_SECRET_STORE=keychain` and the OS keychain is unavailable
- **THEN** a secret write or read SHALL return a clear error naming the missing keychain

#### Scenario: file mode ignores the keychain
- **WHEN** `PMOX_SECRET_STORE=file`
- **THEN** secrets SHALL be written to and read from the file backend regardless of keychain availability

### Requirement: File-backend store

The file backend SHALL persist secrets in a single file at
`<XDG_CONFIG_HOME or ~/.config>/pmox/secrets.yaml`, keyed by canonical
server URL, holding the same logical secrets as the keychain (API token,
node SSH password, node SSH key passphrase). The file SHALL be created
with mode `0600` inside a `0700` directory and written atomically
(temp file + rename). Secrets SHALL NEVER be written into `config.yaml`.

#### Scenario: File is created with restrictive permissions
- **WHEN** the file backend writes a secret and the file does not yet exist
- **THEN** `secrets.yaml` SHALL be created with mode `0600`
- **AND** its parent directory SHALL be mode `0700`

#### Scenario: Secrets are not written to config.yaml
- **WHEN** any secret is stored via the file backend
- **THEN** `config.yaml` SHALL contain no secret values

#### Scenario: Round-trip by URL
- **WHEN** a secret is stored for a canonical URL and later read for the same URL
- **THEN** the stored value SHALL be returned unchanged

### Requirement: Tolerant reads and auto writes

Under `auto`, a read SHALL consult the keychain first and then the file
backend, so a secret stored under either backend is found even if keychain
availability changed between runs. Under `auto`, a write SHALL go to the
keychain when it is available, otherwise to the file backend.

#### Scenario: Read finds a file-stored secret after a keychain appears
- **WHEN** a secret was stored in the file backend, the keychain later becomes available, and the value is not present in the keychain
- **THEN** an `auto` read SHALL still return the file-stored value

#### Scenario: Auto write prefers the keychain
- **WHEN** `auto` is in effect and the keychain is available
- **THEN** a write SHALL store the secret in the keychain

### Requirement: Removal clears every backend

Removing a server's secrets SHALL delete them from all backends (keychain
and file), so no stray secret remains regardless of where it was written.
A backend that has no entry for the secret SHALL be tolerated (no error).

#### Scenario: Remove deletes from both backends
- **WHEN** a server's secrets are removed
- **THEN** the token, node SSH password, and key passphrase entries SHALL be deleted from both the keychain and the file backend
- **AND** a missing entry in either backend SHALL NOT cause an error

### Requirement: Secrets are never logged

Secret values SHALL NOT appear in stdout, stderr, verbose, or debug output
from any secret-store operation.

#### Scenario: No secret in output on error
- **WHEN** a secret-store operation fails
- **THEN** the error and any diagnostic output SHALL NOT contain the secret value

### Requirement: doctor reports the active backend

`pmox doctor` SHALL report which secret backend is in effect and SHALL warn
when the file fallback is active, noting that fallback secrets are stored
as plaintext on disk protected only by file permissions.

#### Scenario: Warn when the file fallback is active
- **WHEN** `pmox doctor` runs and secrets resolve to the file backend
- **THEN** it SHALL emit a warning that the file fallback is in use (plaintext on disk)

#### Scenario: Pass when the keychain is in use
- **WHEN** `pmox doctor` runs and secrets resolve to the keychain
- **THEN** it SHALL report the keychain backend without a warning
