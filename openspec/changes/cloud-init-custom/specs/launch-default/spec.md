## ADDED Requirements

### Requirement: Custom cloud-init option

The `launch.Options` struct SHALL gain a `CloudInitPath string`
field and a `NoSSHKeyCheck bool` field. When `CloudInitPath` is
set, `launch.Run` SHALL route the config phase through the
snippet upload path.

#### Scenario: Options carry the custom cloud-init path
- **WHEN** a caller constructs `launch.Options{CloudInitPath: "/tmp/user-data.yaml"}`
- **AND** calls `launch.Run(ctx, opts)`
- **THEN** the launcher SHALL read the file, validate its contents, upload it via `PostSnippet`, and set `cicustom` in the VM config
- **AND** SHALL NOT set `ciuser` or `sshkeys`

#### Scenario: Empty CloudInitPath preserves built-in behavior
- **WHEN** `CloudInitPath` is empty
- **THEN** the launcher SHALL behave identically to the previous built-in-only path
- **AND** SHALL call `SetConfig` with the built-in kv map

### Requirement: File read and validation happens before clone

The launcher SHALL read and validate the cloud-init file **before**
calling `Clone`, so that a bad file fails fast without creating
an orphan VM on the cluster.

#### Scenario: Invalid file aborts before clone
- **WHEN** the `--cloud-init` file is oversized or not UTF-8
- **THEN** the launcher SHALL return an error before calling `NextID`
- **AND** SHALL NOT issue any PVE API call
