## ADDED Requirements

### Requirement: PostSnippet endpoint

The client SHALL expose `PostSnippet(ctx, node, storage, filename string, content []byte) error`
which uploads a file to `POST /nodes/{node}/storage/{storage}/upload`
as `multipart/form-data` with content type `snippets`.

#### Scenario: Multipart upload contains the expected fields
- **WHEN** `PostSnippet(ctx, "pve1", "local", "pmox-104-user-data.yaml", content)` is called
- **THEN** the client SHALL issue a POST to `/nodes/pve1/storage/local/upload`
- **AND** the request Content-Type SHALL be `multipart/form-data` with a boundary
- **AND** the multipart body SHALL contain a `content` field equal to `snippets`
- **AND** the multipart body SHALL contain a `filename` field equal to `pmox-104-user-data.yaml`
- **AND** the multipart body SHALL contain a `file` part whose body equals `content`

#### Scenario: Upload error propagates
- **WHEN** the PVE API responds with HTTP 500
- **THEN** `PostSnippet` SHALL return an error wrapping `ErrAPIError`

### Requirement: DeleteSnippet endpoint

The client SHALL expose `DeleteSnippet(ctx, node, storage, filename string) error`
which issues `DELETE /nodes/{node}/storage/{storage}/content/{storage}:snippets/{filename}`.

#### Scenario: Delete issues a DELETE
- **WHEN** `DeleteSnippet(ctx, "pve1", "local", "pmox-104-user-data.yaml")` is called
- **THEN** the client SHALL issue `DELETE /nodes/pve1/storage/local/content/local:snippets/pmox-104-user-data.yaml`
- **AND** SHALL return nil on HTTP 200

#### Scenario: 404 maps to ErrNotFound
- **WHEN** the PVE API responds with HTTP 404
- **THEN** `DeleteSnippet` SHALL return an error wrapping `ErrNotFound`

### Requirement: ListStorageContent endpoint

The client SHALL expose `ListStorageContent(ctx, node, storage, contentFilter string) ([]StorageContent, error)`
which issues `GET /nodes/{node}/storage/{storage}/content?content={filter}`
and returns the list of files present in the given content category.

#### Scenario: Snippet listing returns file entries
- **WHEN** `ListStorageContent(ctx, "pve1", "local", "snippets")` is called and the storage contains two snippet files
- **THEN** the returned slice SHALL have two entries
- **AND** each entry SHALL have `Volid`, `Format`, and `Size` populated
