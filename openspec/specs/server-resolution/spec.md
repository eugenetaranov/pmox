## ADDED Requirements

### Requirement: Deterministic server resolution

The binary SHALL provide a `server.Resolve` function that returns
exactly one configured Proxmox server for a given command invocation,
following a fixed five-step precedence ladder.

#### Scenario: --server flag takes highest precedence
- **WHEN** the user passes `--server <url>` and `PMOX_SERVER` is also set
- **AND** multiple servers are configured
- **THEN** the resolver SHALL use the value of `--server` and ignore `PMOX_SERVER`

#### Scenario: PMOX_SERVER used when flag is absent
- **WHEN** `--server` is unset and `PMOX_SERVER` is set to a configured server
- **AND** multiple servers are configured
- **THEN** the resolver SHALL use the value of `PMOX_SERVER`

#### Scenario: Single configured server is auto-selected
- **WHEN** neither `--server` nor `PMOX_SERVER` is set
- **AND** exactly one server is configured
- **THEN** the resolver SHALL return that server without prompting

#### Scenario: Interactive picker when multiple servers and TTY
- **WHEN** neither `--server` nor `PMOX_SERVER` is set
- **AND** more than one server is configured
- **AND** stdin is a terminal
- **THEN** the resolver SHALL present an interactive picker listing the configured URLs in sorted order
- **AND** SHALL return the URL the user selects

#### Scenario: Error when multiple servers and no TTY
- **WHEN** neither `--server` nor `PMOX_SERVER` is set
- **AND** more than one server is configured
- **AND** stdin is not a terminal
- **THEN** the resolver SHALL return an error
- **AND** the error message SHALL instruct the user to pick one with `--server` or `PMOX_SERVER`
- **AND** the error message SHALL list the configured URLs

#### Scenario: Error when no servers are configured
- **WHEN** the config file contains zero servers
- **AND** the command is any command other than `configure`
- **THEN** the resolver SHALL return an error instructing the user to run `pmox configure`

### Requirement: --server flag and PMOX_SERVER env var

The `pmox` root command SHALL accept a persistent `--server` flag and
the resolver SHALL honor the `PMOX_SERVER` environment variable.

#### Scenario: --server is a persistent root flag
- **WHEN** the user runs `pmox --help`
- **THEN** the `--server` flag SHALL appear in the global flags list
- **AND** any subcommand SHALL accept `--server` without redeclaring it

#### Scenario: configure ignores --server and PMOX_SERVER
- **WHEN** the user runs `pmox configure --server https://foo`
- **OR** `pmox configure` with `PMOX_SERVER` set
- **THEN** `configure` SHALL proceed with its interactive flow without consulting either value
- **AND** SHALL NOT error on the presence of the flag or env var

### Requirement: Server input canonicalization

Values supplied via `--server` and `PMOX_SERVER` SHALL be canonicalized
before being matched against the config map, so that users can pass
any reasonable URL form and still hit the stored canonical key.

#### Scenario: Full canonical URL matches exactly
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server https://pve1.lan:8006/api2/json`
- **THEN** the resolver SHALL return the matching server

#### Scenario: URL without path is canonicalized to match
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server https://pve1.lan:8006`
- **THEN** the resolver SHALL canonicalize the input and return the matching server

#### Scenario: URL without port is canonicalized to match
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server https://pve1.lan`
- **THEN** the resolver SHALL canonicalize the input and return the matching server

#### Scenario: Bare hostname is accepted and canonicalized
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server pve1.lan`
- **THEN** the resolver SHALL prepend `https://`, canonicalize, and return the matching server

#### Scenario: Hostname with port is accepted and canonicalized
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server pve1.lan:8006`
- **THEN** the resolver SHALL prepend `https://`, canonicalize, and return the matching server

#### Scenario: No prefix or substring matching
- **WHEN** the config contains `https://pve1.lan:8006/api2/json`
- **AND** the user passes `--server pve`
- **THEN** the resolver SHALL return an error
- **AND** the error message SHALL list the configured URLs verbatim

#### Scenario: Non-matching input surfaces candidates
- **WHEN** the config contains `https://pve1.lan:8006/api2/json` and `https://pve2.lan:8006/api2/json`
- **AND** the user passes `--server https://pve9.lan`
- **THEN** the resolver SHALL return an error stating no configured server matches the input
- **AND** the error SHALL list both configured URLs

### Requirement: Resolved bundle includes URL, Server, and Secret

On successful resolution, the resolver SHALL return a bundle
containing the canonical URL, the `*config.Server` block, and the
token secret fetched from the system keychain.

#### Scenario: Happy-path bundle
- **WHEN** resolution succeeds
- **THEN** the returned `*Resolved` SHALL have a non-empty `URL` that exists in the config map
- **AND** SHALL have a non-nil `Server` pointer
- **AND** SHALL have a non-empty `Secret` fetched from `credstore.Get(URL)`

#### Scenario: Keychain secret missing
- **WHEN** resolution identifies a server by URL
- **AND** `credstore.Get(URL)` returns `credstore.ErrNotFound`
- **THEN** the resolver SHALL return an error
- **AND** the error message SHALL instruct the user to re-run `pmox configure`
- **AND** the error SHALL wrap `exitcode.ErrNotFound`

#### Scenario: Keychain transport failure
- **WHEN** `credstore.Get(URL)` returns any error other than `ErrNotFound`
- **THEN** the resolver SHALL return an error wrapping that error with context identifying the URL
