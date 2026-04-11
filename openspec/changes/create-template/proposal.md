## Why

pmox can launch VMs from a template but has no way to *create* the
template in the first place. Users are sent to the Proxmox web UI
(or SSH into the host) to download a cloud image, `qm importdisk`
it, install `qemu-guest-agent`, and convert to a template — a
multi-step manual ritual that hides exactly the kind of friction
pmox exists to remove. This slice closes the loop: one interactive
command that picks an Ubuntu cloud image, fetches it to a storage,
bakes `qemu-guest-agent` into it via cloud-init, and converts the
result to a template ready for `pmox launch`.

## What Changes

- Add `pmox create-template` subcommand. Interactive-only in this
  slice (non-interactive flags deferred to a follow-up).
- Orchestrate an 8-phase state machine:
  `pick-image → pick-storage → pick-snippets → upload-snippet → download-image → create-vm → start → wait-stopped → detach-cloudinit → convert-template`.
- Fetch the Ubuntu cloud image catalogue from Canonical's
  simplestreams JSON at
  `https://cloud-images.ubuntu.com/releases/streams/v1/com.ubuntu.cloud:released:download.json`,
  filter to amd64 `disk1.img` items, sort newest-first, show the
  top 10 in an interactive picker with the latest LTS as the
  default cursor. Image SHA256 from the same feed is passed to
  PVE's `download-url` endpoint so nothing is trusted from the URL
  alone.
- Target storage picker filtered to `content=images` — this is
  where the VM disk lands.
- Snippets storage handling with three states: (1) a dir-capable
  storage already has `snippets` in its content list → use it
  silently; (2) a dir-capable storage exists without snippets →
  prompt `enable snippets on <name>? [y/N]` and on yes issue
  `PUT /storage/{id}` appending `snippets` to the content list;
  (3) no dir-capable storage exists at all → hard error with a
  message telling the user to create a directory-type storage
  first (pmox will NOT create fresh storage since it can't guess
  host filesystem paths).
- VMID reservation from the Proxmox wiki convention range 9000–9099.
  Scan existing VMIDs on the node, allocate the lowest unused slot
  in the range. If the entire range is occupied, error out with a
  clear message naming the occupied VMIDs. `NextID` is not used.
- PVE 8.0+ is a hard requirement because the `importfrom` disk
  parameter is the API-exposed replacement for `qm importdisk`.
  Detect the cluster version via `GetVersion` up front and error
  cleanly with `PVE 8.0 or later required (found X.Y)` if older.
- Ubuntu cloud images don't ship `qemu-guest-agent` pre-installed,
  so the bake step is mandatory: upload a `#cloud-config` snippet
  that runs `apt-get install -y qemu-guest-agent`, enables the
  service, scrubs `machine-id` / ssh host keys / cloud-init state,
  and powers off. The snippet is embedded in the Go binary and
  uploaded fresh on every run to avoid drift.
- VM creation via `POST /nodes/{node}/qemu` with
  `scsi0=<storage>:0,importfrom=<iso-storage>:iso/<file>` —
  one API call creates the VM and imports the downloaded image as
  its boot disk. Also sets `cicustom=user=<snippets>:snippets/<name>.yaml`,
  `ide2=<storage>:cloudinit`, `serial0=socket`, `vga=serial0`,
  `agent=1`, `ipconfig0=ip=dhcp`, `scsihw=virtio-scsi-single`,
  and the network bridge.
- Boot the VM once, poll `GetStatus` until `status=stopped` (the
  cloud-init snippet ends with `poweroff`), then detach the
  cloud-init drive via `SetConfig` with `delete=ide2` so future
  clones get a fresh cloud-init disk.
- Convert to template via `POST /nodes/{node}/qemu/{vmid}/template`.
- README permissions table gets a new row: `Datastore.Allocate` on
  `/storage/{id}` — required for the `PUT /storage/{id}` call that
  enables snippets on an existing storage. Also `Sys.Modify` on
  `/` for the `download-url` endpoint if PVE's ACL demands it
  (verified during implementation).
- Ubuntu-only in this slice. Debian genericcloud / Fedora Cloud /
  Rocky already ship qga and would skip the install step entirely
  — that's a different bake flow, deferred.
- amd64-only. arm64 images are deferred.

## Capabilities

### New Capabilities
- `create-template`: the `pmox create-template` command, the
  simplestreams catalogue fetcher, the image/storage/snippets
  picker UX, the cloud-init bake snippet, the VMID reservation
  logic for the 9000–9099 range, and the create-template
  state machine that glues `download-url → create-vm → start →
  wait → detach → convert` together.

### Modified Capabilities
- `pveclient-core`: six new client methods are added —
  `CreateVM`, `ConvertToTemplate`, `DownloadURL`, `UploadSnippet`,
  `UpdateStorageContent`, and optionally `ListClusterStorage` (if
  the existing per-node `ListStorage` isn't sufficient for the
  snippets picker). These extend the client surface with the
  write-side endpoints needed to build a template from scratch,
  without changing any existing method contracts.

## Impact

- **New files**: `cmd/pmox/create_template.go` (Cobra wiring and
  flag parsing), `internal/template/build.go` (state machine),
  `internal/template/simplestreams.go` (catalogue fetch + filter),
  `internal/template/cloudinit.go` (embedded bake snippet + name
  generator), `internal/template/storage.go` (snippets-storage
  picker + enable-snippets prompt), `internal/template/vmid.go`
  (9000–9099 allocator). Test siblings for each.
- **New pveclient files**: `internal/pveclient/storage.go` (new —
  houses `DownloadURL`, `UploadSnippet`, `UpdateStorageContent`)
  and additions to `internal/pveclient/vm.go` (`CreateVM`,
  `ConvertToTemplate`).
- **Modified files**: `cmd/pmox/main.go` — register
  `newCreateTemplateCmd()` on the root command. `README.md` —
  add the `Datastore.Allocate` row to the permissions table.
  `ROADMAP.md` — slot `create-template` as slice 10.
- **New dependency**: none. Simplestreams JSON is plain `net/http`
  + `encoding/json`; the cloud-init snippet is a Go `embed`
  string; the interactive picker reuses `internal/tui`
  (`selectOne`) from slice 3.
- **Read-only consumers**: `internal/config`, `internal/credstore`,
  `internal/server`, `internal/tui`.
- **No schema changes**: `config.yaml` is not extended.
- **Cross-slice contract**: templates created by this command are
  indistinguishable from hand-built ones at the API level — they
  appear in `ListTemplates` with `template=1` exactly like any
  other template, so `pmox launch --template` picks them up with
  no extra plumbing. No shared tag convention is needed.
- **Out of scope**: non-interactive mode (`--yes`, `--image`,
  `--target-storage`, `--snippets-storage` flags); Debian / Fedora
  / Rocky / other cloud images; arm64 images; custom disk sizes
  at build time (clones can resize at launch time); template
  deletion or rebuild-in-place; per-release channel selection
  beyond what simplestreams surfaces as "latest".
