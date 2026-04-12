## REMOVED Requirements

### Requirement: UploadSnippet endpoint
**Reason**: The PVE HTTP upload endpoint rejects `content=snippets`
server-side on stock PVE 8.x (the `content` parameter enum is hardcoded
to `iso, vztmpl, import`), so this method never worked against a real
cluster. Snippet uploads now happen via SSH/SFTP through the new
`internal/pvessh` package.
**Migration**: Callers SHALL open a `pvessh.Client` via `pvessh.Dial`
and invoke `(*Client).UploadSnippet(ctx, storagePath, filename, content)`.
The storage path is obtained by calling `GET /storage/{storage}` and
reading the `path` field.

### Requirement: UpdateStorageContent endpoint
**Reason**: This method existed only to append `snippets` to a storage
pool's cluster-wide `content=` list as a prerequisite for the HTTP
snippet upload. With the upload path replaced by SSH/SFTP, mutating
the PVE storage config is no longer needed and was an unwanted side
effect on user clusters.
**Migration**: None. There is no replacement; pmox SHALL NOT mutate
cluster-wide storage configuration. Users whose storage pools already
had `snippets` content enabled are unaffected; users whose pools did
not will simply never see the mutation happen, and SCP still writes
files successfully because the filesystem write is independent of the
PVE storage content whitelist.
