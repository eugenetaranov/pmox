## ADDED Requirements

### Requirement: pvessh package

The binary SHALL ship a new `internal/pvessh` package that wraps
`golang.org/x/crypto/ssh` and `github.com/pkg/sftp` to provide SSH/SFTP
access to a Proxmox VE node, exclusively for writing snippet files into
a storage pool's on-disk `snippets/` directory. No other command
execution or file operations outside the snippets path SHALL be exposed.

#### Scenario: Package exposes Dial, UploadSnippet, Ping, Close
- **WHEN** a caller imports `internal/pvessh`
- **THEN** the package SHALL export `Dial(ctx, cfg) (*Client, error)`, `(*Client).UploadSnippet(ctx, storagePath, filename, content) error`, `(*Client).Ping(ctx) error`, and `(*Client).Close() error`
- **AND** `Dial` SHALL accept a `Config` struct with fields `Host`, `User`, `Password`, `KeyPath`, `KeyPass`, `Insecure`, `KnownHosts`

#### Scenario: Exactly one auth method is required
- **WHEN** `Dial` is called with both `Password` and `KeyPath` set, or with neither set
- **THEN** it SHALL return an error identifying the misconfigured auth before opening any socket

### Requirement: Password authentication

`pvessh.Dial` SHALL support password authentication against the PVE
node using the supplied `User` and `Password` from its `Config`.

#### Scenario: Successful password handshake
- **WHEN** `Dial` is called with valid `User` and `Password`
- **AND** the remote host accepts the credentials
- **THEN** `Dial` SHALL return a non-nil `*Client` and a nil error

#### Scenario: Wrong password returns a typed error
- **WHEN** the remote host rejects the password
- **THEN** `Dial` SHALL return an error whose message identifies an authentication failure against the target host
- **AND** the error SHALL NOT include the password value

### Requirement: Key-file authentication

`pvessh.Dial` SHALL support private-key authentication by reading a
PEM-encoded key file from `KeyPath`, optionally decrypted with
`KeyPass`.

#### Scenario: Unencrypted key succeeds
- **WHEN** `Dial` is called with `KeyPath` pointing at an unencrypted ed25519 key
- **AND** the remote host accepts the resulting public key
- **THEN** `Dial` SHALL return a non-nil `*Client` and a nil error

#### Scenario: Encrypted key with correct passphrase
- **WHEN** `Dial` is called with `KeyPath` at an encrypted key and the matching `KeyPass`
- **THEN** the key SHALL be decrypted in-process and the handshake SHALL succeed

#### Scenario: Encrypted key with missing passphrase
- **WHEN** `Dial` is called with `KeyPath` at an encrypted key and an empty `KeyPass`
- **THEN** `Dial` SHALL return an error clearly stating the key is passphrase-protected

### Requirement: UploadSnippet writes file atomically via SFTP

`(*Client).UploadSnippet(ctx, storagePath, filename, content)` SHALL
ensure the directory `<storagePath>/snippets/` exists via `MkdirAll`,
then write the supplied `content` bytes to
`<storagePath>/snippets/<filename>` using the SFTP subsystem. The write
SHALL be atomic via write-to-temp-and-rename within the same directory,
so a crashed upload never leaves a truncated file.

#### Scenario: Snippets directory is created when missing
- **WHEN** `UploadSnippet` is called with `storagePath="/var/lib/vz"`
- **AND** `/var/lib/vz/snippets` does not exist
- **THEN** the client SHALL create it via SFTP `MkdirAll` before writing

#### Scenario: Existing file is overwritten
- **WHEN** `UploadSnippet` is called with a filename that already exists at the destination
- **THEN** the new content SHALL replace the existing file on success

#### Scenario: Atomic temp-and-rename
- **WHEN** `UploadSnippet` begins writing
- **THEN** it SHALL write to a temporary filename in the same directory
- **AND** SHALL rename the temp file over the destination only after the final byte has been flushed

#### Scenario: Context cancellation aborts the upload
- **WHEN** the caller's context is cancelled mid-upload
- **THEN** the client SHALL close the SFTP session and return the context error
- **AND** SHALL NOT leave the destination filename holding a partially written file

### Requirement: Ping validates connectivity without writing

`(*Client).Ping(ctx)` SHALL verify the SSH/SFTP session is usable by
performing a read-only SFTP `stat` of `/`. It SHALL NOT create, modify,
or delete any file on the remote host.

#### Scenario: Ping succeeds on a working session
- **WHEN** `Ping` is called on a freshly dialed client
- **THEN** it SHALL return nil

#### Scenario: Ping surfaces SFTP subsystem failure
- **WHEN** the remote host disables the SFTP subsystem
- **THEN** `Ping` SHALL return an error identifying the missing SFTP subsystem

### Requirement: Host-key verification strategy

`pvessh.Dial` SHALL verify the remote host key against a pmox-managed
known_hosts file at the path given in `Config.KnownHosts` (default
`~/.config/pmox/known_hosts`). If verification fails and `Insecure` is
false, `Dial` SHALL return an error without completing the handshake.
When `Insecure` is true, host-key verification SHALL be skipped and a
warning SHALL be emitted on stderr by the caller.

#### Scenario: First-seen host is rejected without prompt in library layer
- **WHEN** `Dial` is called against a host whose key is not in `KnownHosts`
- **AND** `Insecure` is false
- **THEN** `Dial` SHALL return an error identifying the unknown host
- **AND** the library SHALL NOT prompt interactively; interactive pinning is the caller's responsibility

#### Scenario: Matching known_hosts entry succeeds
- **WHEN** `KnownHosts` contains a line matching the remote host and key
- **THEN** `Dial` SHALL complete the handshake

#### Scenario: Changed host key is rejected
- **WHEN** `KnownHosts` has an entry for the host but the presented key differs
- **THEN** `Dial` SHALL return an error identifying a host-key mismatch
- **AND** the error message SHALL NOT be silenceable via `Insecure`

#### Scenario: Insecure skips verification entirely
- **WHEN** `Insecure` is true
- **THEN** host-key verification SHALL be skipped and the handshake SHALL proceed regardless of `KnownHosts` contents

### Requirement: pmox-specific known_hosts file

The pmox-managed known_hosts file SHALL live at
`$XDG_CONFIG_HOME/pmox/known_hosts` (or `$HOME/.config/pmox/known_hosts`
if `XDG_CONFIG_HOME` is unset), separate from the user's
`~/.ssh/known_hosts`. The file SHALL have mode `0600` and its parent
directory SHALL be `0700`.

#### Scenario: File is created with secure permissions
- **WHEN** pmox writes a new host-key pin for the first time
- **THEN** the parent directory SHALL exist with mode `0700`
- **AND** the file SHALL exist with mode `0600`

#### Scenario: User ssh_known_hosts is not touched
- **WHEN** pmox pins or reads any host key
- **THEN** `~/.ssh/known_hosts` SHALL NOT be read, written, or modified

### Requirement: Resolve storage path via PVE API

`pmox create-template` SHALL resolve the on-disk `path` for a storage
pool by calling `GET /storage/{storage}` and reading the `path` field
on every run, rather than caching it in local config.

#### Scenario: Path is fetched fresh per run
- **WHEN** `create-template` needs the snippets destination path
- **THEN** it SHALL call `GET /storage/{storage}` and use the returned `path` field concatenated with `/snippets/`

#### Scenario: Missing path field is a hard error
- **WHEN** the storage pool response contains no `path` field (e.g. block-only storage)
- **THEN** `create-template` SHALL exit with an error explaining the storage cannot hold snippet files
