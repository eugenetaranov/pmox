## ADDED Requirements

### Requirement: Snippet storage is independent from disk storage

pmox SHALL treat the storage that holds cloud-init snippets as a
separate concern from the storage that holds VM disk images. The
launcher SHALL resolve a snippet storage and a disk storage
independently, and neither resolution SHALL assume they refer to the
same PVE storage pool.

#### Scenario: Disk storage and snippet storage differ
- **WHEN** `server.Storage` is `vm-data` and `server.SnippetStorage` is `local`
- **THEN** the launcher SHALL place the VM disk on `vm-data`
- **AND** the launcher SHALL upload the cloud-init snippet to `local`
- **AND** the resulting `cicustom` value SHALL reference `local:snippets/pmox-<vmid>-user-data.yaml`

#### Scenario: Disk-storage content validation is unchanged
- **WHEN** the launcher checks disk storage
- **THEN** it SHALL continue to use the existing `SupportsVMDisks` check against `opts.Storage`
- **AND** SHALL NOT require the disk storage to contain `snippets`

#### Scenario: Snippet-storage content validation targets the snippet storage
- **WHEN** the launcher validates snippet storage
- **THEN** it SHALL call `ValidateStorage(ctx, client, node, opts.SnippetStorage)`
- **AND** SHALL NOT validate `opts.Storage` for `snippets` content

### Requirement: Configure picks or enables a snippet storage

The `pmox configure` command SHALL resolve a snippet storage for each
configured server, independent from the disk storage, and persist it
as `server.snippet_storage` in `config.yaml`.

#### Scenario: Exactly one storage supports snippets
- **WHEN** `pmox configure` is run and exactly one entry in `ListStorage` has `snippets` in its content types
- **THEN** configure SHALL save that entry's name as `server.snippet_storage` without prompting
- **AND** SHALL print the chosen name to stdout

#### Scenario: Multiple storages support snippets
- **WHEN** `pmox configure` is run and more than one entry in `ListStorage` has `snippets` in its content types
- **THEN** configure SHALL show a TUI picker titled `Snippet storage` listing the candidates
- **AND** SHALL save the selected name as `server.snippet_storage`

#### Scenario: No storage supports snippets — enable on existing dir storage
- **WHEN** `pmox configure` is run, no storage has `snippets` in its content types, and at least one storage is of type `dir`, `nfs`, `cifs`, or `cephfs`
- **THEN** configure SHALL prompt `enable snippets on "<name>"? [Y/n]` defaulting to `local` when present
- **AND** on `y` or Enter SHALL call `UpdateStorageContent` with the existing content list plus `snippets`
- **AND** on success SHALL save `<name>` as `server.snippet_storage`
- **AND** on decline SHALL print the manual remediation and leave `snippet_storage` empty

#### Scenario: No storage supports snippets and no dir-backed storage exists
- **WHEN** `pmox configure` is run and zero storages can host snippets at all
- **THEN** configure SHALL print the manual remediation naming `/etc/pve/storage.cfg` and pointing at `pmox configure` to re-run
- **AND** SHALL NOT prompt
- **AND** SHALL save the rest of the config so credentials are not lost

### Requirement: `--snippet-storage` override on launch and clone

`pmox launch` and `pmox clone` SHALL accept `--snippet-storage <name>`
as an override for `server.snippet_storage` on a single invocation.
The override SHALL NOT affect disk-storage resolution.

#### Scenario: Flag overrides configured snippet storage
- **WHEN** `server.SnippetStorage` is `local` and the user runs `pmox launch --snippet-storage nfs-shared`
- **THEN** the launcher SHALL validate, upload to, and set `cicustom` against `nfs-shared`
- **AND** the VM disk SHALL still be placed on `server.Storage`

#### Scenario: Flag independent from `--storage`
- **WHEN** `pmox launch --storage vm-data --snippet-storage local` is run
- **THEN** the VM disk SHALL land on `vm-data`
- **AND** the snippet SHALL upload to `local`

### Requirement: Fallback when snippet storage is unset

The launcher SHALL fall back to `opts.Storage` as the snippet storage
when `server.SnippetStorage` is empty and no `--snippet-storage` flag
is given, and SHALL emit a stderr warning naming the fallback storage
and suggesting `pmox configure` as the permanent fix.

#### Scenario: Empty snippet storage falls back with a warning
- **WHEN** an old `config.yaml` with no `snippet_storage` key is loaded and `pmox launch` is invoked without `--snippet-storage`
- **THEN** the launcher SHALL use `opts.Storage` as the snippet storage
- **AND** stderr SHALL contain `warning: no snippet_storage configured; falling back to "<name>". run 'pmox configure' to set it permanently`

#### Scenario: Fallback still validates content type
- **WHEN** the fallback path is taken and `opts.Storage` does not have `snippets` in its content types
- **THEN** the launcher SHALL return the existing "storage does not have 'snippets' in its content types" error before upload

### Requirement: Snippet upload uses SFTP, not the PVE upload endpoint

The launcher SHALL upload cloud-init snippet files via SFTP into the
snippet storage's on-disk `snippets/` directory and SHALL NOT call
PVE's `POST /nodes/{node}/storage/{storage}/upload` endpoint for
snippets. PVE rejects `content=snippets` on that endpoint with a
hardcoded 400 ("value 'snippets' does not have a value in the
enumeration 'iso, vztmpl, import'"), so the HTTP path is unusable.

#### Scenario: Snippet upload routes through SFTP
- **WHEN** `launch.Run` reaches the snippet-upload phase
- **THEN** it SHALL call `Client.GetStoragePath(opts.SnippetStorage)` to
  resolve the storage's on-disk path
- **AND** it SHALL call `opts.UploadSnippet(ctx, storagePath, snippet.Filename(vmid), cloudInitBytes)`
- **AND** it SHALL NOT call any HTTP upload endpoint for the snippet

#### Scenario: Launch requires SSH credentials for snippet upload
- **WHEN** `pmox launch` or `pmox clone` is run against a server whose `node_ssh` block is missing
- **THEN** the command SHALL fail with an error directing the user to run `pmox configure` to add SSH credentials
- **AND** the failure SHALL surface before any PVE clone is issued, so no orphan VM is left behind

#### Scenario: PostSnippet is removed
- **WHEN** the codebase is built after this change
- **THEN** `pveclient.Client` SHALL NOT expose a `PostSnippet` method
- **AND** no caller SHALL reference it

### Requirement: Delete cleanup resolves storage from the `cicustom` value

`pmox delete` SHALL continue to extract the snippet storage from the
destroyed VM's `cicustom` value (not from `server.Storage` or
`server.SnippetStorage`). This is the existing behavior; this
requirement exists only to lock it in so the split does not regress it.

#### Scenario: Delete routes cleanup to the snippet storage, not the disk storage
- **WHEN** the VM being deleted has `cicustom=user=local:snippets/pmox-104-user-data.yaml` and `server.Storage=vm-data`
- **THEN** the delete command SHALL issue `DeleteSnippet(node, "local", "pmox-104-user-data.yaml")`
- **AND** SHALL NOT issue any `DeleteSnippet` call against `vm-data`
