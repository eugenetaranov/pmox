## ADDED Requirements

### Requirement: `--cloud-init` flag

The `launch` and `clone` commands SHALL accept a `--cloud-init <path>`
flag. When set, the launcher SHALL upload the file contents as a
PVE snippet and reference it via the VM's `cicustom` config key.

#### Scenario: Built-in cloud-init is skipped when `--cloud-init` is set
- **WHEN** `pmox launch --cloud-init user-data.yaml web1` is invoked
- **THEN** the config SetConfig call SHALL NOT contain `ciuser` or `sshkeys`
- **AND** SHALL contain `cicustom=user=<storage>:snippets/pmox-<vmid>-user-data.yaml`

#### Scenario: Snippet upload precedes SetConfig
- **WHEN** the launcher reaches the config phase with `--cloud-init` set
- **THEN** the launcher SHALL call `PostSnippet` before `SetConfig`
- **AND** a `PostSnippet` failure SHALL abort the launch before any config is pushed

### Requirement: Full-replace semantics

When `--cloud-init` is set, the uploaded file SHALL be the VM's
entire user-data. The launcher SHALL NOT inject, merge, or layer
any built-in cloud-init content on top of the provided file.

#### Scenario: SetConfig for custom cloud-init omits built-in keys
- **WHEN** the launcher builds the config map for a custom-cloud-init launch
- **THEN** the map SHALL NOT contain keys `ciuser`, `cipassword`, or `sshkeys`
- **AND** SHALL contain `cicustom`, `agent`, `memory`, `cores`, `name`, `ipconfig0`

### Requirement: Snippet storage content validation

Before uploading, the launcher SHALL verify the resolved storage
has `snippets` in its content types. The check runs at launch
time, not at configure time.

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

The launcher SHALL emit a stderr warning when the cloud-init file has no `ssh_authorized_keys:` substring, and SHALL NOT block the launch on this condition.

#### Scenario: Missing ssh_authorized_keys emits warning
- **WHEN** the file content has no `ssh_authorized_keys:` substring
- **THEN** stderr SHALL contain `warning: --cloud-init file has no ssh_authorized_keys; you may not be able to SSH in`
- **AND** the launch SHALL proceed

#### Scenario: `--no-ssh-key-check` silences the warning
- **WHEN** the caller passes `--no-ssh-key-check`
- **THEN** the warning SHALL NOT be emitted

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

The delete command SHALL remove any pmox-owned snippet referenced by the destroyed VM's `cicustom` config value, as a best-effort step after the destroy task completes.

#### Scenario: Delete removes the referenced snippet
- **WHEN** the VM being deleted has `cicustom=user=local:snippets/pmox-104-user-data.yaml`
- **THEN** after the destroy task completes, the delete command SHALL issue `DeleteSnippet(node, "local", "pmox-104-user-data.yaml")`

#### Scenario: Delete does not call snippet cleanup for built-in cloud-init VMs
- **WHEN** the VM being deleted has no `cicustom` key in its config
- **THEN** the delete command SHALL NOT call `DeleteSnippet`

#### Scenario: Cleanup failure is a warning, not an error
- **WHEN** `DeleteSnippet` returns an error (other than `ErrNotFound`)
- **THEN** the delete command SHALL print a warning to stderr naming the VMID and the error
- **AND** SHALL exit 0

#### Scenario: Already-missing snippet is not an error
- **WHEN** `DeleteSnippet` returns `ErrNotFound`
- **THEN** the delete command SHALL treat it as success with no warning

### Requirement: Example cloud-init file

The repository SHALL ship `examples/cloud-init.yaml` with a
minimal working snippet that includes an `ssh_authorized_keys:`
entry and the `qemu-guest-agent` package.

#### Scenario: Example file passes our own validators
- **WHEN** the test suite reads `examples/cloud-init.yaml`
- **THEN** `ValidateContent` SHALL return nil
- **AND** the file content SHALL contain `ssh_authorized_keys:`
- **AND** the file content SHALL contain `qemu-guest-agent`
