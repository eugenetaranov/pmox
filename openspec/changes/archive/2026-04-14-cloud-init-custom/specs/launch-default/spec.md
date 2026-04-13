## MODIFIED Requirements

### Requirement: Cloud-init config always routes through a snippet

`launch.Run` SHALL always read a per-server cloud-init file,
validate it, upload it via `PostSnippet`, and set `cicustom` on
the target VM. The launcher SHALL NOT set the built-in
`ciuser`, `cipassword`, or `sshkeys` keys. `BuildBuiltinKV` is
removed; `BuildCustomKV` is the only config-builder.

The resolved cloud-init path is populated into
`launch.Options.CloudInitPath` by the caller (the CLI layer)
from `config.CloudInitPath(canonicalURL)` before `Run` is
invoked. `launch.Options` SHALL NOT carry a `NoSSHKeyCheck`
field.

#### Scenario: Happy-path launch uploads a snippet and sets cicustom
- **WHEN** a caller constructs `launch.Options{CloudInitPath: "/tmp/cloud-init.yaml", Storage: "local", ...}`
- **AND** the file at that path exists, is valid UTF-8, ≤64 KiB, and the resolved storage supports snippets
- **AND** calls `launch.Run(ctx, opts)`
- **THEN** the launcher SHALL call `PostSnippet` with the file contents and filename `pmox-<vmid>-user-data.yaml`
- **AND** SHALL call `SetConfig` with a kv map whose `cicustom` equals `user=local:snippets/pmox-<vmid>-user-data.yaml`
- **AND** the kv map SHALL NOT contain `ciuser`, `cipassword`, or `sshkeys`

#### Scenario: BuildCustomKV is the only config-builder
- **WHEN** the launcher is inspected at the config phase
- **THEN** there SHALL be no code path that produces a config map via `BuildBuiltinKV`

### Requirement: File read and validation happens before clone

The launcher SHALL read and validate the cloud-init file
**before** calling `NextID` or `Clone`, so that a bad or
missing file fails fast without creating an orphan VM on the
cluster.

#### Scenario: Missing file aborts before clone
- **WHEN** `opts.CloudInitPath` points at a non-existent path
- **THEN** the launcher SHALL return an error wrapping the underlying not-exist error
- **AND** the error SHALL include the substring `pmox configure --regen-cloud-init`
- **AND** SHALL NOT issue any PVE API call

#### Scenario: Invalid file aborts before clone
- **WHEN** `opts.CloudInitPath` points at a binary or oversized file
- **THEN** the launcher SHALL return a validation error before calling `NextID`
- **AND** SHALL NOT issue any PVE API call
