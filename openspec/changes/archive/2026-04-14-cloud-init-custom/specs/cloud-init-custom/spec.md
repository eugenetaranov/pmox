## ADDED Requirements

### Requirement: Configure-time cloud-init file generation

The `configure` command SHALL write a starter cloud-init file
to a per-server path under the user's config directory the
first time a server is configured. The file path SHALL be
derived from the canonical server URL and the user's config
directory; it SHALL NOT be stored in `config.yaml`.

#### Scenario: First-time configure writes a starter file
- **WHEN** `pmox configure` completes for a server at `https://192.168.0.185:8006` and the target path does not exist
- **THEN** a file SHALL be created at `<user-config-dir>/pmox/cloud-init/192.168.0.185-8006.yaml`
- **AND** the file SHALL contain the configured SSH pubkey
- **AND** the file SHALL contain the configured default user
- **AND** the file SHALL pass `snippet.ValidateContent`
- **AND** the configure command SHALL print the path to stdout

#### Scenario: Existing file is not overwritten
- **WHEN** `pmox configure` completes for a server and a file already exists at the resolved path
- **THEN** the existing file contents SHALL NOT be modified
- **AND** stdout SHALL contain `cloud-init template already exists at <path> — not overwriting`

#### Scenario: Slug derivation is stable
- **WHEN** `CloudInitPath` is called with a canonical URL of the form `https://<host>:<port>`
- **THEN** the resolved filename base SHALL equal `<host>-<port>.yaml`
- **AND** if the URL omits an explicit port, the slug SHALL use `8006`

### Requirement: `--regen-cloud-init` subflag

The `configure` command SHALL accept a `--regen-cloud-init`
flag which rewrites the cloud-init file for a selected server
from the in-binary template, using the SSH pubkey and default
user already stored in `config.yaml`. This flag SHALL NOT walk
the full interactive configure flow.

#### Scenario: Regenerate overwrites after confirmation
- **WHEN** `pmox configure --regen-cloud-init` is invoked, a single server is already configured, and the cloud-init file exists
- **THEN** the command SHALL prompt `cloud-init file already exists at <path>. Overwrite? [y/N]`
- **AND** on `y` SHALL rewrite the file atomically from the template using `srv.User` and `srv.SSHPubkey`
- **AND** on anything else SHALL leave the file alone and print `aborted; no changes`

#### Scenario: Regenerate writes a missing file without prompting
- **WHEN** `pmox configure --regen-cloud-init` is invoked and the target file does not exist
- **THEN** the command SHALL write the file without prompting
- **AND** stdout SHALL contain the resulting path

#### Scenario: Regenerate against an unconfigured server fails
- **WHEN** `pmox configure --regen-cloud-init` is invoked and no servers are configured
- **THEN** the command SHALL return an error naming `pmox configure` as the remediation

### Requirement: Full-replace semantics

Every pmox-launched VM's user-data SHALL come from the
per-server cloud-init file. The launcher SHALL NOT set the
built-in cloud-init keys `ciuser`, `cipassword`, or `sshkeys`
on any launch or clone; those concerns live in the cloud-init
file on disk.

#### Scenario: SetConfig omits built-in cloud-init keys
- **WHEN** the launcher builds the config map for any launch or clone
- **THEN** the map SHALL NOT contain keys `ciuser`, `cipassword`, or `sshkeys`
- **AND** SHALL contain `cicustom`, `agent`, `memory`, `cores`, `name`, `ipconfig0`

#### Scenario: Snippet upload precedes SetConfig
- **WHEN** the launcher reaches the config phase
- **THEN** the launcher SHALL call `PostSnippet` before `SetConfig`
- **AND** a `PostSnippet` failure SHALL abort the launch before any config is pushed

### Requirement: Missing cloud-init file aborts launch

The launcher SHALL abort before any PVE API call when the
per-server cloud-init file cannot be read. The error message
SHALL name the resolved path and SHALL suggest `pmox configure
--regen-cloud-init` as a remediation.

#### Scenario: Missing file aborts before NextID
- **WHEN** `pmox launch` is invoked and the resolved cloud-init path does not exist
- **THEN** the launcher SHALL return an error before calling `NextID`
- **AND** the error message SHALL include the resolved path
- **AND** the error message SHALL include the substring `pmox configure --regen-cloud-init`

#### Scenario: Unreadable file aborts before NextID
- **WHEN** the resolved cloud-init path exists but cannot be read (e.g. permission denied)
- **THEN** the launcher SHALL return an error wrapping the underlying read error
- **AND** SHALL NOT issue any PVE API call

### Requirement: Snippet storage content validation

Before uploading, the launcher SHALL verify the resolved
storage has `snippets` in its content types. The check runs at
launch time, not at configure time.

#### Scenario: Missing snippets content fails with actionable message
- **WHEN** the resolved storage's content types are `iso,vztmpl,rootdir,images`
- **THEN** the launcher SHALL return an error before calling `PostSnippet`
- **AND** the error message SHALL name the storage
- **AND** SHALL list the current content types
- **AND** SHALL mention `/etc/pve/storage.cfg`
- **AND** SHALL mention `--storage` as an alternative fix
- **AND** SHALL include a link to `https://pve.proxmox.com/wiki/Storage`

#### Scenario: Storage with snippets passes validation
- **WHEN** the resolved storage's content types include `snippets`
- **THEN** the launcher SHALL proceed with the upload

#### Scenario: Configure does not validate snippet support
- **WHEN** `pmox configure` is invoked and the user completes the flow
- **THEN** the configure command SHALL NOT check storage content types for snippet support

### Requirement: Snippet file validation

Before uploading, the launcher SHALL validate the cloud-init
file contents.

#### Scenario: Empty file fails
- **WHEN** the file is 0 bytes
- **THEN** the launcher SHALL return an error containing `empty`

#### Scenario: Oversized file fails
- **WHEN** the file is larger than 64 KiB
- **THEN** the launcher SHALL return an error naming the size and the 64 KiB limit

#### Scenario: Non-UTF-8 file fails
- **WHEN** the file contains bytes that are not valid UTF-8
- **THEN** the launcher SHALL return an error containing `not valid UTF-8`

### Requirement: SSH key warning

The launcher SHALL emit an unconditional stderr warning when
the cloud-init file has no `ssh_authorized_keys:` substring,
and SHALL NOT block the launch on this condition.

#### Scenario: Missing ssh_authorized_keys emits warning
- **WHEN** the file content has no `ssh_authorized_keys:` substring
- **THEN** stderr SHALL contain `warning: cloud-init file <path> has no ssh_authorized_keys; you may not be able to SSH in`
- **AND** the launch SHALL proceed

#### Scenario: File with ssh_authorized_keys emits no warning
- **WHEN** the file contains the substring `ssh_authorized_keys:`
- **THEN** no warning SHALL be emitted regardless of where the substring appears in the file

### Requirement: Snippet filename convention

The launcher SHALL name uploaded snippets
`pmox-<vmid>-user-data.yaml` where `<vmid>` is the numeric VMID
of the target VM.

#### Scenario: Filename format
- **WHEN** the launcher uploads a snippet for VM 104
- **THEN** the `filename` field in the multipart upload SHALL equal `pmox-104-user-data.yaml`
- **AND** the resulting `cicustom` value SHALL reference `snippets/pmox-104-user-data.yaml`

### Requirement: Snippet cleanup on delete

The delete command SHALL remove any pmox-owned snippet
referenced by the destroyed VM's `cicustom` config value, as a
best-effort step after the destroy task completes.

#### Scenario: Delete removes the referenced snippet
- **WHEN** the VM being deleted has `cicustom=user=local:snippets/pmox-104-user-data.yaml`
- **THEN** after the destroy task completes, the delete command SHALL issue `DeleteSnippet(node, "local", "pmox-104-user-data.yaml")`

#### Scenario: Delete skips cleanup when cicustom is absent
- **WHEN** the VM being deleted has no `cicustom` key in its config
- **THEN** the delete command SHALL NOT call `DeleteSnippet`

#### Scenario: Cleanup failure is a warning, not an error
- **WHEN** `DeleteSnippet` returns an error (other than `ErrNotFound`)
- **THEN** the delete command SHALL print a warning to stderr naming the VMID and the error
- **AND** SHALL exit 0

#### Scenario: Already-missing snippet is not an error
- **WHEN** `DeleteSnippet` returns `ErrNotFound`
- **THEN** the delete command SHALL treat it as success with no warning

### Requirement: Example cloud-init template

The repository SHALL ship `examples/cloud-init.yaml` as the
source of truth for the template embedded in the pmox binary
and rendered by `pmox configure`. It SHALL include an
`ssh_authorized_keys:` entry and the `qemu-guest-agent` package.

#### Scenario: Example file passes our own validators
- **WHEN** the test suite reads `examples/cloud-init.yaml`
- **THEN** `ValidateContent` SHALL return nil
- **AND** the file content SHALL contain `ssh_authorized_keys:`
- **AND** the file content SHALL contain `qemu-guest-agent`

#### Scenario: Rendered template passes our own validators
- **WHEN** `RenderTemplate("ubuntu", "ssh-ed25519 AAAA...")` is called
- **THEN** the returned bytes SHALL pass `ValidateContent`
- **AND** SHALL contain the substring `ssh-ed25519 AAAA...`
- **AND** SHALL contain `ssh_authorized_keys:`
