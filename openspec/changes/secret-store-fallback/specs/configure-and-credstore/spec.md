## MODIFIED Requirements

### Requirement: SSH secrets in the keyring

SSH secrets SHALL be stored through the resolved secret store (the OS
keychain when available, otherwise the file backend — see the
`secret-store` capability), keyed by canonicalized server URL plus a
suffix that identifies the secret kind:

- `<url>#node_ssh_password` — present when `node_ssh.auth == "password"`
- `<url>#node_ssh_key_passphrase` — present when `node_ssh.auth == "key"` AND the key is passphrase-protected

The choice of backend is transparent to the configure flow: it calls the
same `credstore` API regardless of which backend is active.

#### Scenario: Password is stored under the node_ssh_password account
- **WHEN** the configure flow completes with password auth
- **THEN** `credstore.GetNodeSSHPassword(<url>)` SHALL return the entered password from whichever backend is active

#### Scenario: Unencrypted key stores no secret
- **WHEN** the configure flow completes with an unencrypted key
- **THEN** the active backend SHALL have no `node_ssh_key_passphrase` entry for that server

#### Scenario: Password auth works without a keychain
- **WHEN** the configure flow completes with password auth on a host with no available keychain
- **THEN** the password SHALL be persisted to the file backend and `credstore.GetNodeSSHPassword(<url>)` SHALL return it on a later run

#### Scenario: Remove cleans up SSH secrets from every backend
- **WHEN** the user runs `pmox configure --remove <url>` (or `pmox config delete-context`)
- **THEN** any `node_ssh_password` and `node_ssh_key_passphrase` entries for that URL SHALL be deleted from every backend (keychain and file) alongside the API-token entry
- **AND** an orphan (missing) entry in any backend SHALL be tolerated the same way the API-token remove already tolerates orphans
