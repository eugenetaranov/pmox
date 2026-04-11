## ADDED Requirements

### Requirement: `pmox create-template` command

The CLI SHALL expose `pmox create-template` which interactively
picks an Ubuntu cloud image, downloads it to a user-chosen PVE
storage, boots it once to install `qemu-guest-agent` via cloud-init,
and converts the result into a Proxmox VM template ready for
`pmox launch`.

#### Scenario: Happy path produces a ready template
- **WHEN** `pmox create-template` is invoked against a fake PVE server returning canned success responses
- **THEN** the command SHALL exit 0
- **AND** stdout SHALL name the created template's VMID and human name
- **AND** the 8-phase state machine SHALL have issued calls in order: `GetVersion`, catalogue fetch, `ListStorage`, `UploadSnippet`, `DownloadURL`, `CreateVM`, `Start`, `WaitTask`, `GetStatus` (polled), `SetConfig` (detach cloud-init drive), `ConvertToTemplate`

#### Scenario: Non-interactive TTY errors out
- **WHEN** `pmox create-template` is invoked without a TTY on stdin
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL say that non-interactive mode is not yet supported and point at the future flag-based mode

### Requirement: PVE 8.0 minimum

The command SHALL verify the cluster is running Proxmox VE 8.0 or
later before issuing any write-path call, because the VM creation
step depends on the `import-from` disk parameter which was added in
qemu-server 8.0.

#### Scenario: PVE 7.x is rejected cleanly
- **WHEN** `GetVersion` reports a version string that parses to a major version below 8
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL contain `PVE 8.0 or later required` and the detected version

#### Scenario: PVE 8.x proceeds
- **WHEN** `GetVersion` reports `8.1.4` or similar
- **THEN** the command SHALL proceed to the catalogue fetch phase

### Requirement: Canonical simplestreams catalogue fetch

The command SHALL fetch Ubuntu's official simplestreams JSON from
`https://cloud-images.ubuntu.com/releases/streams/v1/com.ubuntu.cloud:released:download.json`,
filter to amd64 `disk1.img` items, group by release, and return
the most recent version of each release sorted newest-first.

#### Scenario: Top 10 images surfaced
- **WHEN** the simplestreams JSON is parsed successfully
- **THEN** the picker SHALL show at most 10 entries
- **AND** entries SHALL be sorted by release version-date descending
- **AND** entries SHALL include only amd64 `disk1.img` items

#### Scenario: Latest LTS is the default cursor
- **WHEN** the picker is shown
- **THEN** the initial cursor SHALL rest on the latest LTS release (a release whose label ends in `LTS`)
- **AND** if no LTS exists in the top 10, the cursor SHALL rest on entry 0

#### Scenario: Simplestreams fetch failure surfaces clearly
- **WHEN** the HTTPS GET against the simplestreams URL fails (DNS, TLS, HTTP 5xx)
- **THEN** the command SHALL exit with `ExitRemote`
- **AND** the error message SHALL name the URL and wrap the underlying network error

#### Scenario: Checksum is carried through to download
- **WHEN** a catalogue entry is selected
- **THEN** its `sha256` field SHALL be captured
- **AND** the later `DownloadURL` call SHALL pass `checksum=<sha256>` and `checksum-algorithm=sha256` as form parameters

### Requirement: Target storage picker

The command SHALL prompt the user to pick a PVE storage to hold
the final VM disk, filtered to storages with `content=images`.

#### Scenario: Only images-capable storages shown
- **WHEN** the target storage picker is rendered
- **THEN** only entries whose content list includes `images` SHALL appear
- **AND** storages with `active=0` or `enabled=0` SHALL be excluded

#### Scenario: No images storage available
- **WHEN** zero storages support `images`
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL direct the user to enable `images` content on a storage

### Requirement: Snippets storage handling

The command SHALL ensure a storage with `content=snippets` exists
on the cluster before issuing the VM create, because the bake flow
attaches a `cicustom` user-data file that must live on such storage.
When no storage has snippets enabled, the command SHALL prompt the
user and, on confirmation, issue `UpdateStorageContent` to append
`snippets` to an existing directory-capable storage's content list.

#### Scenario: Snippets already enabled — silent use
- **WHEN** a storage reports `content` containing `snippets`
- **THEN** the command SHALL use it for the cicustom upload
- **AND** SHALL NOT prompt the user

#### Scenario: Dir-capable storage without snippets — prompt to enable
- **WHEN** no storage has snippets enabled
- **AND** at least one dir-capable storage (type `dir`, `nfs`, `cifs`, `cephfs`, `glusterfs`) exists
- **THEN** the command SHALL prompt `enable snippets on <name>? [y/N]`
- **AND** on `y`, SHALL call `UpdateStorageContent` with the current content list plus `snippets`
- **AND** on `N` or empty, SHALL exit with `ExitConfig` and a message explaining that snippets storage is required

#### Scenario: No dir-capable storage exists
- **WHEN** zero dir-capable storages exist on the cluster
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL instruct the user to create a directory-type storage on their cluster
- **AND** the command SHALL NOT attempt to create storage itself

### Requirement: Cloud-init bake snippet

The command SHALL ship a Go-embedded `#cloud-config` user-data
file that installs `qemu-guest-agent`, enables the service,
scrubs identifiers that must not be baked into a template, and
powers off. The snippet SHALL be uploaded fresh on every run via
`UploadSnippet` so stale copies on the cluster never affect the
build.

#### Scenario: Snippet content is correct
- **WHEN** the embedded snippet is read
- **THEN** it SHALL include `packages: [qemu-guest-agent]`
- **AND** its `runcmd` SHALL enable the agent service
- **AND** its `runcmd` SHALL run `cloud-init clean --logs --seed`
- **AND** its `runcmd` SHALL truncate `/etc/machine-id` and remove `/var/lib/dbus/machine-id` and `/etc/ssh/ssh_host_*`
- **AND** its `runcmd` SHALL end with `poweroff`

#### Scenario: Snippet is uploaded fresh on every run
- **WHEN** the command reaches the upload phase
- **THEN** it SHALL call `UploadSnippet` with a fixed filename (e.g. `pmox-qga-bake.yaml`) on every invocation
- **AND** SHALL NOT check whether the file already exists before uploading

### Requirement: VMID reservation in 9000–9099 range

The command SHALL reserve a VMID from the Proxmox wiki convention
range 9000–9099 for the new template. It SHALL scan existing VMs
on the target node, pick the lowest unused VMID in the range, and
error cleanly if the whole range is occupied.

#### Scenario: Lowest unused VMID is picked
- **WHEN** VMIDs 9000 and 9001 already exist on the node
- **THEN** the command SHALL reserve 9002
- **AND** SHALL NOT call `NextID`

#### Scenario: Range full errors out
- **WHEN** all 100 VMIDs in 9000–9099 are occupied
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL name the range and suggest freeing one by deleting unused templates

### Requirement: Image download via download-url

The command SHALL download the selected cloud image to the chosen
PVE storage via `DownloadURL`, passing the image URL, filename,
sha256 checksum, and `content=import`. PVE 9's storage plugin
only accepts sources for `import-from` from storages whose
content list includes `import`, and the filename SHALL use the
`.qcow2` extension (PVE's import content regex is
`\.(ova|ovf|qcow2|raw|vmdk)` — `.img` is rejected by
`download-url` as an invalid extension, even though Ubuntu cloud
images are qcow2 internally).

#### Scenario: Download call shape
- **WHEN** the download phase runs
- **THEN** the client SHALL call `DownloadURL` with `content=import`, `url=<simplestreams URL>`, `filename=<stable name ending in .qcow2>`, `checksum=<sha256>`, `checksum-algorithm=sha256`
- **AND** SHALL wait for the returned UPID task to reach `stopped` via `WaitTask`

#### Scenario: Download failure is cleanable
- **WHEN** `DownloadURL` or its task fails
- **THEN** the command SHALL exit with `ExitRemote`
- **AND** no VM SHALL have been created yet (the download phase precedes `CreateVM`)

### Requirement: VM creation with import-from

The command SHALL create the template-building VM with a single
`CreateVM` call whose `scsi0` parameter uses the `import-from`
syntax to import the just-downloaded cloud image as the VM's boot
disk in one step.

#### Scenario: CreateVM form contains import-from and required keys
- **WHEN** `CreateVM` is called
- **THEN** the form body SHALL contain `vmid=<reserved>`, `name=<template name>`, `memory=2048`, `cores=2`, `cpu=host`, `agent=1`, `serial0=socket`, `vga=serial0`, `scsihw=virtio-scsi-single`, `boot=order=scsi0`, `ipconfig0=ip=dhcp`, `net0=virtio,bridge=<configured-bridge>`, `ide2=<target-storage>:cloudinit`, `cicustom=user=<snippets-storage>:snippets/pmox-qga-bake.yaml`
- **AND** `scsi0` SHALL equal `<target-storage>:0,import-from=<download-storage>:import/<downloaded-filename>`

### Requirement: Boot, wait-for-stop, detach, convert

After `CreateVM`, the command SHALL start the VM once, poll
`GetStatus` until the cloud-init `poweroff` has landed, detach the
cloud-init drive so future clones get a fresh one, and finally
convert the VM to a template.

#### Scenario: Wait loop polls until stopped
- **WHEN** the VM is started
- **THEN** the command SHALL poll `GetStatus` every 5 seconds
- **AND** SHALL return success on the first poll reporting `status=stopped`
- **AND** SHALL enforce a default 10 minute wait budget with an informative timeout error

#### Scenario: Cloud-init drive is detached before conversion
- **WHEN** the VM has powered off
- **THEN** the command SHALL issue `SetConfig` with `delete=ide2`
- **AND** SHALL then call `ConvertToTemplate` on the same VMID

#### Scenario: Conversion error leaves VM tagged for cleanup
- **WHEN** `ConvertToTemplate` returns an error
- **THEN** the command SHALL return an error message naming the VMID
- **AND** SHALL instruct the user to delete it via the Proxmox UI (since `pmox delete` is in a later slice)

### Requirement: No auto-rollback on partial failure

Like `pmox launch`, `pmox create-template` SHALL NOT auto-delete
or roll back VMs on failures after `CreateVM`. The half-built VM
remains on the cluster for the user to inspect or remove.

#### Scenario: Boot failure leaves VM on the cluster
- **WHEN** the `Start` call or its task fails
- **THEN** the command SHALL return an error naming the VMID
- **AND** SHALL NOT issue any `Delete` call

### Requirement: Verbose server-resolution log line

When `-v` is active, the command SHALL emit one stderr line
identifying which server was selected and why, matching the D-T4
format used by `pmox launch`.

#### Scenario: Verbose run logs the selected server
- **WHEN** `pmox -v create-template` is invoked
- **THEN** stderr SHALL contain one line matching `using server <url> (<reason>)`
- **AND** the line SHALL appear before the first PVE API call

### Requirement: Exit code mapping

The command SHALL map error classes to the typed exit codes from
`internal/exitcode`:

- `ErrUnauthorized` → `ExitAuth`
- `ErrNotFound` → `ExitConfig`
- `ErrAPIError` → `ExitRemote`
- `ErrTimeout` / `context.DeadlineExceeded` → `ExitTimeout`
- any other error → `ExitGeneric`

#### Scenario: 401 from CreateVM surfaces ExitAuth
- **WHEN** `CreateVM` returns an error wrapping `ErrUnauthorized`
- **THEN** the process SHALL exit with `ExitAuth`

#### Scenario: Wait timeout surfaces ExitTimeout
- **WHEN** the wait-stopped phase exceeds its budget
- **THEN** the process SHALL exit with `ExitTimeout`
