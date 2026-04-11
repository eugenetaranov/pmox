## ADDED Requirements

### Requirement: Interactive configure command

The binary SHALL provide a `pmox configure` subcommand that walks the
user through entering credentials for a Proxmox VE server, validates
those credentials against the API, and persists them to the config file
and the system keychain.

#### Scenario: Successful first-time configure
- **WHEN** the user runs `pmox configure` with no servers previously configured
- **AND** enters a valid HTTPS URL, token ID, and token secret
- **AND** the credentials validate against `GET /version`
- **THEN** the binary SHALL prompt for default node, template, storage, bridge, SSH key, and user
- **AND** SHALL write the server entry to `~/.config/pmox/config.yaml`
- **AND** SHALL store the secret in the system keychain under service `pmox` and account equal to the canonicalized URL
- **AND** SHALL print `configured server <url>`

#### Scenario: Invalid credentials
- **WHEN** the user enters credentials that fail `GET /version` with HTTP 401
- **THEN** the binary SHALL print an unauthorized error referring to the token ID
- **AND** SHALL NOT write anything to the config file or keychain
- **AND** SHALL exit with code `ExitUnauthorized`

#### Scenario: Token secret is never echoed
- **WHEN** the user types the token secret at the prompt
- **THEN** the terminal SHALL NOT echo the input
- **AND** the secret SHALL NOT appear in any log output, including under `--debug`

### Requirement: URL canonicalization

All Proxmox API URLs SHALL be canonicalized to a single normalized form
before being stored in the config file or used as a keychain account
name, so that the same server cannot be configured twice under different
URL spellings.

#### Scenario: Canonical form is enforced
- **WHEN** the user enters `HTTPS://PVE.HOME.LAN:8006/api2/json/`
- **THEN** the canonical form SHALL be `https://pve.home.lan:8006/api2/json`
- **AND** that form SHALL be used as both the config map key and keychain account

#### Scenario: Default port is added when missing
- **WHEN** the user enters `https://pve.home.lan/api2/json`
- **THEN** the canonical form SHALL be `https://pve.home.lan:8006/api2/json`

#### Scenario: Path is added when missing
- **WHEN** the user enters `https://pve.home.lan:8006`
- **THEN** the canonical form SHALL be `https://pve.home.lan:8006/api2/json`

#### Scenario: Non-https schemes are rejected
- **WHEN** the user enters `http://pve.home.lan:8006/api2/json`
- **THEN** canonicalization SHALL fail with an error stating that pmox requires HTTPS

#### Scenario: Wrong path is rejected
- **WHEN** the user enters `https://pve.home.lan:8006/some/other/path`
- **THEN** canonicalization SHALL fail with an error pointing at the expected URL shape

### Requirement: Token ID format validation

The configure flow SHALL validate that the entered token ID matches the
PVE token format `user@realm!tokenname` before attempting to validate
the credentials.

#### Scenario: Valid token ID is accepted
- **WHEN** the user enters `pmox@pve!homelab`
- **THEN** the prompt SHALL accept it and proceed

#### Scenario: Invalid token ID re-prompts
- **WHEN** the user enters `pmox` (missing realm and token name)
- **THEN** the prompt SHALL re-display with an error explaining the expected format
- **AND** SHALL allow up to 3 attempts before aborting with `ExitUserError`

### Requirement: TLS verification with insecure auto-fallback

The configure flow SHALL attempt credential validation with full TLS
verification first. If verification fails because of an untrusted or
self-signed certificate, the flow SHALL automatically retry with
verification disabled, save the server with `insecure: true`, and warn
the user clearly.

#### Scenario: Strict TLS succeeds
- **WHEN** the PVE server presents a CA-signed certificate
- **THEN** validation SHALL succeed on the first attempt
- **AND** the saved config SHALL have `insecure: false`

#### Scenario: TLS fallback to insecure
- **WHEN** the PVE server presents a self-signed certificate
- **AND** the strict-TLS request fails with a verification error
- **THEN** the binary SHALL retry with TLS verification disabled
- **AND** SHALL print a warning to stderr explaining what happened, that the certificate was not trusted, and how to re-enable verification by editing the config file
- **AND** SHALL save the server with `insecure: true`

#### Scenario: Insecure retry also fails
- **WHEN** the strict-TLS request fails for a TLS reason
- **AND** the insecure retry also fails (e.g. network error or 401)
- **THEN** the binary SHALL surface the more actionable error and exit non-zero
- **AND** SHALL NOT save anything to the config file or keychain

### Requirement: Auto-discovery during configure

After successful credential validation, the configure flow SHALL fetch
available nodes, templates, storages, and network bridges from the API
and present them as numbered picker menus, with a free-text fallback
when any individual API call fails.

#### Scenario: Node picker shows API results
- **WHEN** `GET /nodes` returns a list of cluster nodes
- **THEN** the prompt SHALL display them as a numbered list
- **AND** SHALL accept either a number, a typed name, or empty input (defaulting to the first entry)

#### Scenario: Template picker stores VMID, not name
- **WHEN** the user picks a template from the auto-discovered list
- **THEN** the saved config field `template` SHALL be the VMID (e.g. `9000`), not the human-readable name

#### Scenario: Auto-discovery failure falls back to free text
- **WHEN** any of the auto-discovery calls fails for any reason (timeout, permissions, API error)
- **THEN** the corresponding prompt SHALL fall back to a plain free-text input with no menu
- **AND** the configure flow SHALL NOT abort because of the discovery failure

### Requirement: Config file persistence

Server configuration SHALL be persisted to a YAML file at
`$XDG_CONFIG_HOME/pmox/config.yaml` (or
`$HOME/.config/pmox/config.yaml` if `XDG_CONFIG_HOME` is unset), with
file mode `0600` and parent directory mode `0700`.

#### Scenario: File is created with secure permissions
- **WHEN** `pmox configure` saves a new server for the first time
- **THEN** the parent directory SHALL exist with mode `0700`
- **AND** the file SHALL exist with mode `0600`

#### Scenario: Schema is keyed by canonical URL
- **WHEN** the file is read by a YAML parser
- **THEN** the top-level shape SHALL be `servers: { <canonical-url>: { token_id, node, template, storage, bridge, ssh_key, user, insecure } }`

#### Scenario: Atomic writes
- **WHEN** the file is being saved
- **THEN** the write SHALL be atomic via temp-file-and-rename
- **AND** a partial write SHALL NOT leave a truncated file

### Requirement: Keychain credential storage

Token secrets SHALL be stored in the system keychain via
`github.com/zalando/go-keyring`, under service name `pmox` and account
name equal to the canonicalized server URL. Secrets SHALL NEVER be
written to the config file or any log output.

#### Scenario: Set and Get round-trip
- **WHEN** a secret is stored via `credstore.Set(url, secret)`
- **AND** later retrieved via `credstore.Get(url)`
- **THEN** the returned secret SHALL exactly match what was stored

#### Scenario: Keychain unavailable on Linux
- **WHEN** `credstore.Set` is called on a Linux system with no Secret Service running
- **THEN** the returned error SHALL clearly state that the system keychain is unavailable and that pmox requires gnome-keyring or KWallet on Linux

#### Scenario: Get on missing URL returns sentinel
- **WHEN** `credstore.Get(url)` is called for a URL that has no entry
- **THEN** the returned error SHALL satisfy `errors.Is(err, credstore.ErrNotFound)`

### Requirement: Configure --list

The configure subcommand SHALL accept a `--list` flag that prints all
configured server URLs to stdout, sorted, one per line, without printing
any token IDs or secrets.

#### Scenario: List with multiple servers
- **WHEN** two servers are configured
- **AND** the user runs `pmox configure --list`
- **THEN** the output SHALL be exactly two lines, each containing one canonical URL, sorted lexicographically

#### Scenario: List with no servers
- **WHEN** no servers are configured
- **AND** the user runs `pmox configure --list`
- **THEN** the output SHALL be `no servers configured`
- **AND** the exit code SHALL be 0

### Requirement: Configure --remove

The configure subcommand SHALL accept a `--remove <url>` flag that
removes the corresponding entry from both the config file and the
keychain.

#### Scenario: Remove an existing server
- **WHEN** the user runs `pmox configure --remove https://pve.home.lan:8006/api2/json`
- **AND** that server is currently configured
- **THEN** the binary SHALL canonicalize the URL, remove the config entry, save the file, remove the keychain entry, and print `removed <url>`

#### Scenario: Remove a non-configured server
- **WHEN** the user runs `pmox configure --remove <url>` for a URL that is not in the config file
- **THEN** the binary SHALL exit with code `ExitNotFound`
- **AND** SHALL print a clear error explaining the URL is not configured

#### Scenario: Remove tolerates an orphan keychain entry
- **WHEN** the config file lists a URL but the keychain has no matching entry
- **AND** the user runs `pmox configure --remove <url>`
- **THEN** the config entry SHALL be removed and the command SHALL succeed without error

### Requirement: Re-configure prompts to overwrite

The interactive `pmox configure` flow SHALL prompt the user to confirm overwriting whenever the entered URL is already present in the config file, and SHALL abort with no changes unless the user explicitly accepts.

#### Scenario: User accepts overwrite
- **WHEN** the user is prompted `Server <url> is already configured. Overwrite? [y/N]:`
- **AND** types `y`
- **THEN** the flow SHALL continue and replace the existing entry on save

#### Scenario: User rejects overwrite
- **WHEN** the user types `n`, presses enter, or types anything other than `y`/`Y`
- **THEN** the flow SHALL abort with no changes to the config file or keychain
- **AND** SHALL exit with code 0

## MODIFIED Requirements

### Requirement: Typed exit codes

The binary SHALL exit with a distinct integer code for each broad
failure category, and `internal/exitcode.From(err)` SHALL recognize the
typed error sentinels exposed by `internal/credstore` and
`internal/pveclient` introduced in this slice.

#### Scenario: Unauthorized maps to ExitUnauthorized
- **WHEN** any command returns an error for which `errors.Is(err, pveclient.ErrUnauthorized)` is true
- **THEN** `exitcode.From(err)` SHALL return `ExitUnauthorized`

#### Scenario: Network failure maps to ExitNetworkError
- **WHEN** any command returns an error for which `errors.Is(err, pveclient.ErrNetwork)` is true
- **THEN** `exitcode.From(err)` SHALL return `ExitNetworkError`

#### Scenario: Credstore not-found maps to ExitNotFound
- **WHEN** any command returns an error for which `errors.Is(err, credstore.ErrNotFound)` is true
- **THEN** `exitcode.From(err)` SHALL return `ExitNotFound`
