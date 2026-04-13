## ADDED Requirements

### Requirement: SSH credential prompts in configure

The interactive `pmox configure` flow SHALL, after successful API
credential validation and auto-discovery, prompt the user for the SSH
credentials used to upload cloud-init snippets to the PVE node:

1. SSH username (default `root`)
2. Auth method: password (default) or key file
3. Either the password or the key file path (and optional passphrase)

These prompts SHALL appear on first-time configure AND on re-configure
of an existing server record.

#### Scenario: Default username is root
- **WHEN** the user is prompted for the SSH username and presses enter
- **THEN** the saved `ssh_user` SHALL be `root`

#### Scenario: Password is the default auth method
- **WHEN** the user is prompted `Authenticate with (p)assword or (k)ey file? [p]:` and presses enter
- **THEN** the flow SHALL proceed down the password path

#### Scenario: Password is never echoed
- **WHEN** the user types the SSH password at the prompt
- **THEN** the terminal SHALL NOT echo the input
- **AND** the password SHALL NOT appear in any log output, including under `--debug`

#### Scenario: Key-file path is captured
- **WHEN** the user selects `k` and enters `/Users/e/.ssh/pve_ed25519`
- **THEN** the saved `ssh_key` field SHALL be that path
- **AND** the flow SHALL ask whether the key is passphrase-protected
- **AND** if yes, SHALL read the passphrase without echoing it

### Requirement: SSH validation at configure time

Before persisting SSH credentials, the configure flow SHALL perform a
real SSH handshake against the PVE node (the same host as the API URL,
port 22) and SHALL run `pvessh.Ping` to confirm the SFTP subsystem is
reachable with the supplied credentials. On failure the flow SHALL
re-prompt, matching the existing API-token validation pattern.

#### Scenario: Successful handshake persists credentials
- **WHEN** validation succeeds
- **THEN** the flow SHALL print `Verifying SSH connectivity to <host>:22... ok`
- **AND** SHALL persist the new fields to config and keyring

#### Scenario: Auth failure re-prompts
- **WHEN** the SSH handshake fails with an authentication error
- **THEN** the flow SHALL print a clear error identifying the auth failure
- **AND** SHALL re-prompt for the auth method and credential
- **AND** SHALL NOT write anything to config or keychain until validation passes

#### Scenario: Unreachable host re-prompts
- **WHEN** the SSH dial times out or the host rejects the TCP connection
- **THEN** the flow SHALL print an error identifying the unreachable host
- **AND** SHALL allow the user to retry or abort

#### Scenario: First-seen host-key prompt
- **WHEN** the PVE node's host key is not pinned in the pmox known_hosts file
- **THEN** the flow SHALL print the presented fingerprint and prompt `Are you sure you want to continue connecting (yes/no)?`
- **AND** on `yes` SHALL persist the fingerprint to `~/.config/pmox/known_hosts` before continuing
- **AND** on anything else SHALL abort the configure flow without saving

### Requirement: SSH fields in the YAML config

The server record in `~/.config/pmox/config.yaml` SHALL gain an optional
nested `node_ssh` block holding the credentials pmox uses to SSH into
the Proxmox node itself (distinct from the top-level `ssh_pubkey` field,
which is the public key injected into launched VMs' cloud-init):

```yaml
node_ssh:
  user: root                 # string, default "root"
  auth: password             # "password" or "key"
  key_path: /path/to/key     # string, present only when auth == "key"
```

No SSH secret SHALL ever be stored in the YAML file.

#### Scenario: Password mode persists only non-secret fields
- **WHEN** the user completes configure with password auth
- **THEN** the saved YAML SHALL contain `node_ssh.user` and `node_ssh.auth: password`
- **AND** SHALL NOT contain any field holding the password

#### Scenario: Key mode persists the key path
- **WHEN** the user completes configure with key auth
- **THEN** the saved YAML SHALL contain `node_ssh.user`, `node_ssh.auth: key`, and `node_ssh.key_path: <path>`
- **AND** SHALL NOT contain any passphrase material

### Requirement: SSH secrets in the keyring

SSH secrets SHALL be stored in the system keychain under the existing
`pmox` service, keyed by canonicalized server URL plus a suffix that
identifies the secret kind:

- `<url>#node_ssh_password` — present when `node_ssh.auth == "password"`
- `<url>#node_ssh_key_passphrase` — present when `node_ssh.auth == "key"` AND the key is passphrase-protected

#### Scenario: Password is stored under the node_ssh_password account
- **WHEN** the configure flow completes with password auth
- **THEN** `credstore.GetNodeSSHPassword(<url>)` SHALL return the entered password

#### Scenario: Unencrypted key stores no keyring secret
- **WHEN** the configure flow completes with an unencrypted key
- **THEN** the keyring SHALL have no `node_ssh_key_passphrase` entry for that server

#### Scenario: Remove cleans up SSH secrets
- **WHEN** the user runs `pmox configure --remove <url>`
- **THEN** any `node_ssh_password` and `node_ssh_key_passphrase` entries for that URL SHALL be deleted from the keyring alongside the existing API-token entry
- **AND** an orphan keyring entry SHALL be tolerated the same way the API-token remove already tolerates orphans

### Requirement: Resolved server exposes SSH fields

The resolved server struct returned by server-resolution code SHALL
include the SSH credential fields so downstream commands can open an
SSH session without re-reading config. The fields carry a `NodeSSH`
prefix to distinguish them from the top-level `SSHPubkey` used for
cloud-init injection:

- `NodeSSHUser string`
- `NodeSSHAuth string` — `"password"` or `"key"`
- `NodeSSHPassword string` — populated only when `NodeSSHAuth == "password"`
- `NodeSSHKeyPath string` — populated only when `NodeSSHAuth == "key"`
- `NodeSSHKeyPassphrase string` — populated only when the key is passphrase-protected

A `HasNodeSSH()` helper on the resolved struct reports whether the
record is fully populated and ready for SSH use.

#### Scenario: Password-mode resolve populates NodeSSHPassword
- **WHEN** the resolver loads a password-mode server record
- **THEN** `NodeSSHUser`, `NodeSSHAuth == "password"`, and `NodeSSHPassword` SHALL be set
- **AND** `NodeSSHKeyPath` and `NodeSSHKeyPassphrase` SHALL be empty strings

#### Scenario: Key-mode resolve populates NodeSSHKeyPath
- **WHEN** the resolver loads a key-mode server record
- **THEN** `NodeSSHUser`, `NodeSSHAuth == "key"`, and `NodeSSHKeyPath` SHALL be set
- **AND** `NodeSSHPassword` SHALL be empty

### Requirement: Missing SSH fields block snippet-writing commands

`pmox create-template`, `pmox launch`, and `pmox clone` SHALL detect
missing SSH fields on the resolved server and exit with a clear error
instructing the user to re-run `pmox configure`. Commands that do not
upload snippets (e.g. `pmox list`, `pmox info`, `pmox delete`) SHALL
continue to work without SSH credentials.

#### Scenario: Launch fails fast without SSH fields
- **WHEN** the user runs `pmox launch` against a server record missing the `node_ssh` block
- **THEN** the command SHALL exit with `ExitConfig` before any clone is issued
- **AND** SHALL print a message telling the user to run `pmox configure` to add SSH credentials

#### Scenario: create-template fails fast with a clear message
- **WHEN** the user runs `pmox create-template` against a server record missing the SSH fields
- **THEN** the command SHALL exit non-zero before any API call
- **AND** SHALL print a message telling the user to run `pmox configure` to add SSH credentials

### Requirement: Snippet storage resolution during configure

The `pmox configure` flow SHALL resolve a snippet storage for each
configured server, independent from the disk storage, and persist it
as `server.snippet_storage` in `config.yaml`. The snippet storage is
the pool into which cloud-init files are uploaded; it does not have
to match the pool that holds VM disks.

#### Scenario: Exactly one storage supports snippets
- **WHEN** `pmox configure` is run and exactly one entry returned by `ListStorage` has `snippets` in its content types
- **THEN** configure SHALL save that entry's name as `server.snippet_storage` without prompting

#### Scenario: Multiple storages support snippets
- **WHEN** more than one entry has `snippets` in its content types
- **THEN** configure SHALL show a TUI picker titled `Snippet storage` listing the candidates
- **AND** SHALL save the selected name as `server.snippet_storage`

#### Scenario: No storage supports snippets — offer to enable
- **WHEN** no storage has `snippets` in its content types and at least one storage is of type `dir`, `nfs`, `cifs`, or `cephfs`
- **THEN** configure SHALL prompt `enable snippets on "<name>"? [Y/n]` defaulting to `local` when present
- **AND** on confirmation SHALL call `UpdateStorageContent` to append `snippets` to the pool's content list
- **AND** SHALL save `<name>` as `server.snippet_storage`
- **AND** on decline SHALL print manual remediation pointing at `/etc/pve/storage.cfg` and leave `snippet_storage` empty

#### Scenario: No snippet-capable storage at all
- **WHEN** zero storages can host snippets (no existing `snippets` content and no dir-backed pool)
- **THEN** configure SHALL print the manual remediation and SHALL NOT prompt
- **AND** SHALL still save the remaining credentials so the user can re-run after fixing storage.cfg

### Requirement: --ssh-insecure escape hatch

The root `pmox` command SHALL accept a persistent `--ssh-insecure` flag
(also available via env var `PMOX_SSH_INSECURE=1`) that disables
host-key verification for all SSH sessions in the process. Setting it
SHALL emit a warning to stderr at the moment of first use.

#### Scenario: Flag disables host-key check
- **WHEN** the user passes `--ssh-insecure`
- **AND** the destination host key is not in the pmox known_hosts file
- **THEN** the SSH handshake SHALL proceed without verification
- **AND** a warning SHALL be printed to stderr at first use in the session

#### Scenario: Flag is accepted on configure
- **WHEN** `pmox configure --ssh-insecure` is run against a host whose key is not yet pinned
- **THEN** configure SHALL NOT persist any host-key fingerprint
- **AND** SHALL still complete the credential validation
