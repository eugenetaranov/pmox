## MODIFIED Requirements

### Requirement: pmox-owned config files
devbox-setup's own config files SHALL live in its source tree and SHALL be embedded in the binary. `task bootstrap:sync` SHALL copy only the tack-roles files (`mcp-sync`, `mcp-catalog/*.spec`, `SOURCE`) into their own directory, and SHALL NOT change devbox-setup's config files.

#### Scenario: Sync keeps pmox config
- **WHEN** `task bootstrap:sync` runs
- **THEN** only the tack-roles files change

#### Scenario: Conf files reach the VM
- **WHEN** an item writes one of devbox-setup's config files
- **THEN** its contents come from the binary, with no share directory on the VM

### Requirement: Checksum-pinned downloads
Release binaries and vendor install scripts that steps download SHALL be verified against a SHA-256 pinned in the catalog when the vendor publishes a stable artifact. A mismatch SHALL fail that item, naming the file, and SHALL leave nothing installed. Downloads without a pin SHALL be listed in a test, so new ones are a reviewed choice.

#### Scenario: Corrupted download
- **WHEN** a pinned download does not match its SHA-256
- **THEN** the item fails naming the file, and its target is not changed
