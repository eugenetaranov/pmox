# Spec Delta

## MODIFIED Requirements

### Requirement: pvessh package

The binary SHALL ship a new `internal/pvessh` package that wraps
`golang.org/x/crypto/ssh` and `github.com/pkg/sftp` to provide SSH/SFTP
access to a Proxmox VE node. It is used for writing snippet files into a
storage pool's on-disk `snippets/` directory, the file operations pmox
already performs on the node, and running a command on the node through
`Run`, which `pmox router` uses for `pct`.

#### Scenario: Package exposes Dial, UploadSnippet, Ping, Close
- **WHEN** a caller imports `internal/pvessh`
- **THEN** the package SHALL export `Dial(ctx, cfg) (*Client, error)`, `(*Client).UploadSnippet(ctx, storagePath, filename, content) error`, `(*Client).Ping(ctx) error`, `(*Client).Run(ctx, command, stdin, stdout, stderr) error`, and `(*Client).Close() error`
- **AND** `Dial` SHALL accept a `Config` struct with fields `Host`, `User`, `Password`, `KeyPath`, `KeyPass`, `Insecure`, `KnownHosts`

#### Scenario: Exactly one auth method is required
- **WHEN** `Dial` is called with both `Password` and `KeyPath` set, or with neither set
- **THEN** it SHALL return an error identifying the misconfigured auth before opening any socket

## ADDED Requirements

### Requirement: Run executes a command without a terminal

`(*Client).Run` SHALL run one command on the node in a new session without allocating a terminal. It SHALL copy `stdin` to the command and stream its output to `stdout` and `stderr` as it arrives. A non-zero exit SHALL come back as an error carrying the exit code. Cancelling `ctx` SHALL close the session.

#### Scenario: Stdin reaches the command
- **WHEN** `Run` is called with command `cat` and stdin `abc`
- **THEN** stdout SHALL receive `abc`

#### Scenario: Exit code
- **WHEN** the command exits with status 3
- **THEN** `Run` SHALL return an error from which the caller can read exit code 3

#### Scenario: Cancellation
- **WHEN** `ctx` is cancelled while the command runs
- **THEN** `Run` SHALL return promptly with the context's error
