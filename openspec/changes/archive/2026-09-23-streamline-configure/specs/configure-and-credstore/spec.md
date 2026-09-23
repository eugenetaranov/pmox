## ADDED Requirements

### Requirement: Lenient API URL normalization

`configure` SHALL accept the API address in the forms a user naturally
types and normalize it to the canonical `https://<host>:<port>/api2/json`
form. Input MUST be accepted whether or not it includes a scheme, a port,
or a path.

#### Scenario: Bare IP address

- **WHEN** the user enters `10.0.0.5`
- **THEN** it is normalized to `https://10.0.0.5:8006/api2/json`

#### Scenario: Bare hostname

- **WHEN** the user enters `pve.lan`
- **THEN** it is normalized to `https://pve.lan:8006/api2/json`

#### Scenario: Host with explicit port

- **WHEN** the user enters `10.0.0.5:8007`
- **THEN** the explicit port is honored, yielding `https://10.0.0.5:8007/api2/json`

#### Scenario: IPv6 literal

- **WHEN** the user enters `[fd00::1]:8006`
- **THEN** it is normalized to `https://[fd00::1]:8006/api2/json`

#### Scenario: http scheme is upgraded

- **WHEN** the user enters `http://pve.lan:8006`
- **THEN** the scheme is upgraded to `https`, a one-line note is printed, and the result is `https://pve.lan:8006/api2/json`

#### Scenario: Pasted web-UI URL

- **WHEN** the user enters `https://pve.lan:8006/#v1:0:=qemu%2F100`
- **THEN** the path/query/fragment are stripped, yielding `https://pve.lan:8006/api2/json`

#### Scenario: Unparseable input

- **WHEN** the user enters input that cannot be parsed as a host
- **THEN** normalization fails with an error describing the problem

### Requirement: Endpoint reachability probe before credentials

`configure` SHALL contact the normalized endpoint with an
unauthenticated request before prompting for the API token, and SHALL
classify the outcome. The API token MUST NOT be sent to an endpoint that
has not been confirmed to be a PVE API.

#### Scenario: Endpoint is a reachable PVE API

- **WHEN** the probe receives an HTTP 200 or 401 from `/api2/json/version`
- **THEN** the endpoint is treated as reachable and `configure` proceeds to the token prompt

#### Scenario: TLS certificate is untrusted

- **WHEN** the probe fails TLS verification
- **THEN** `configure` warns that the certificate could not be verified, falls back to insecure mode, and proceeds
- **AND** the insecure decision is reused for the subsequent credential validation without a second prompt

#### Scenario: Endpoint reachable but not a PVE API

- **WHEN** the probe reaches the host but the response is not a PVE API response (e.g. 404 or HTML)
- **THEN** `configure` reports that the address does not look like a PVE API and re-asks for the URL

### Requirement: URL re-ask loop on unreachable host

`configure` SHALL, when the reachability probe reports a network failure
(connection refused, timeout, no route, or DNS failure) in interactive
mode, print a specific error and re-prompt for the URL rather than
aborting. The loop MUST offer a clean exit.

#### Scenario: Host not responding, then corrected

- **WHEN** the user enters an address whose host/port does not respond
- **THEN** `configure` prints an error naming the host and port and prompts for the URL again
- **AND** when the user then enters a reachable address, `configure` proceeds

#### Scenario: User aborts the loop

- **WHEN** the user submits a blank line or sends EOF/Ctrl-C at the URL prompt
- **THEN** `configure` exits cleanly without saving

#### Scenario: Non-interactive path fails fast

- **WHEN** the URL is supplied non-interactively (flag/env) and the probe reports the host unreachable
- **THEN** `configure` fails immediately with the classified error and does not enter a re-ask loop

### Requirement: Single-option discovery auto-select

`configure` SHALL auto-select the sole option during auto-discovery: when
a resource list (node, template, storage, or bridge) contains exactly one
option, it is selected automatically and the choice reported instead of
presenting a picker. Lists with more than one option MUST still prompt.

#### Scenario: Only one node exists

- **WHEN** discovery finds exactly one node
- **THEN** that node is selected automatically and printed (e.g. "Using node: pve") with no picker shown

#### Scenario: Multiple options still prompt

- **WHEN** discovery finds more than one storage pool
- **THEN** the picker is shown so the user can choose

### Requirement: SSH bootstrap key generate, select, or browse

The SSH public-key step SHALL offer, in interactive mode, a top-level
choice to generate a new dedicated bootstrap keypair, select an existing
key, or browse the filesystem for one. Only the public key path is
persisted to `ssh_pubkey`.

#### Scenario: Generate a new bootstrap key

- **WHEN** the user chooses to generate a new key
- **THEN** `configure` creates an ed25519 OpenSSH keypair at a predictable path with the private key mode `0600` and the public key mode `0644`, and stores the public key path in `ssh_pubkey`

#### Scenario: Generation does not clobber an existing key

- **WHEN** the target key path already exists
- **THEN** `configure` does not overwrite it without explicit confirmation

#### Scenario: Select an existing key

- **WHEN** the user chooses to use an existing key
- **THEN** `configure` presents the `~/.ssh/*.pub` picker and stores the chosen public key path

#### Scenario: Browse for a key outside ~/.ssh

- **WHEN** the user chooses to browse
- **THEN** `configure` presents a filesystem picker and, on selecting a private key, uses its adjacent `.pub`, or uses a selected `.pub` directly

#### Scenario: Non-interactive keeps default selection

- **WHEN** input is non-interactive (`--no-input`)
- **THEN** `configure` uses the suggested/default key without generating or showing a picker
