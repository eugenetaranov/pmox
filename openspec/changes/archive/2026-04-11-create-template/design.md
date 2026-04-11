## Context

Slices 1–4 shipped the binary skeleton, credstore, server resolver,
and pveclient-core. Slice 5 (`launch-default`, in flight) lets
users launch VMs from an existing template. But pmox has no way
to *build* that template — users still have to log into the
Proxmox web UI (or SSH into the host) to download a cloud image,
`qm importdisk` it, install `qemu-guest-agent`, and convert to a
template.

This slice removes that friction. `pmox create-template` fetches
Canonical's official Ubuntu image catalogue, downloads a selected
image to a user-chosen storage, boots it once with a cloud-init
snippet that bakes in `qemu-guest-agent`, and converts the result
to a template — all via the PVE HTTP API, no SSH to the host.

The key enabler is PVE 8.0's `import-from` disk parameter, which
turns "import a qcow2 as a VM disk" from a host-shell operation
(`qm importdisk`) into a single API field on `POST /nodes/{node}/
qemu`. Without it, building templates remotely would require a
much more invasive approach (e.g. client-side ISO authoring).

## Goals / Non-Goals

**Goals:**
- One interactive command that takes a user from "I have a fresh
  Proxmox cluster" to "I can `pmox launch`" without touching the
  web UI or host shell.
- Canonical simplestreams as the source of truth for available
  images — no hand-maintained list of URLs in the pmox binary.
- Honest checksum verification at the PVE side (via
  `download-url`'s `checksum` param sourced from simplestreams).
- Clean failure modes: every pre-VM-creation phase can fail with
  no cleanup needed; post-creation failures leave a visible,
  user-deletable VM on the cluster.
- Reuse the existing `internal/tui` picker from slice 3 so the UX
  matches `pmox configure` without a new dependency.

**Non-Goals:**
- Non-interactive / scripted mode. Deferred to a follow-up slice
  once the interactive flow is proven. This slice is deliberately
  TTY-only, same shape as `pmox configure`.
- Non-Ubuntu distros. Debian genericcloud, Fedora Cloud, and
  Rocky 9 ship `qemu-guest-agent` pre-installed, so their bake
  flow is materially simpler (no `apt-get install`). Supporting
  them is a real feature, but the decision of "which distros
  does pmox ship catalogue support for" is a product question
  that should land as its own slice.
- arm64 images. Everything in this slice is amd64-only.
- Custom disk sizes at template-build time. The cloud image
  default (~2.2 GB) is preserved; clones resize at launch time
  per slice 5.
- Template deletion, rebuild-in-place, or versioning. A template
  built by this command is just another template — delete it
  through the Proxmox UI if you want to rebuild.
- Any changes to `config.yaml`. No new config keys.

## Decisions

### D1. Package layout — `cmd/pmox/create_template.go` + `internal/template`

`create-template` is complex enough to split out of `main.go`,
matching the `configure.go` / `launch.go` precedent. The Cobra
wiring lives in `cmd/pmox/create_template.go` (flag parsing, exit
code mapping, verbose log line); the orchestration lives in
`internal/template/build.go` so it's testable without Cobra.

```
cmd/pmox/create_template.go    // Cobra command + flag parsing
internal/template/build.go     // Run(ctx, Options) (*Result, error)
internal/template/simplestreams.go  // catalogue fetch + filter + picker rows
internal/template/cloudinit.go // embedded bake snippet + name helpers
internal/template/storage.go   // snippets-storage picker + enable prompt
internal/template/vmid.go      // 9000–9099 allocator
```

Rejected: a single `internal/template.go`. The build phases are
discrete enough that splitting them keeps each file under ~150
lines and each test file focused. Matches the `internal/launch/`
shape from slice 5.

Rejected: `internal/createtemplate/`. The directory name has to
be importable as an identifier; `template` is Go stdlib-ish but
not conflicting (we don't import `text/template` in this
package). If the conflict ever bites, we rename to `tmplbuild`.

### D2. Linear state machine, not an FSM type

`internal/template.Run(ctx, opts) (*Result, error)` is one
top-to-bottom function with labelled phases, same shape as
`internal/launch.Run`:

```go
func Run(ctx context.Context, opts Options) (*Result, error) {
    // 0. pve version check
    // 1. fetch simplestreams
    // 2. pick image (interactive)
    // 3. list storage, pick target (interactive)
    // 4. pick/prepare snippets storage (interactive + maybe PUT)
    // 5. upload bake snippet
    // 6. reserve vmid (9000–9099)
    // 7. download-url + WaitTask
    // 8. CreateVM + WaitTask
    // 9. Start + WaitTask
    // 10. poll GetStatus until status=stopped
    // 11. SetConfig delete=ide2 (detach cloud-init)
    // 12. ConvertToTemplate
}
```

Rejected: breaking each phase out as its own method on a struct.
Linear code is easier to read, easier to test with a stateful
`httptest.Server`, and matches the shape of `launch.Run`.

### D3. Catalogue source — simplestreams JSON, not HTML scraping

Canonical publishes a machine-readable simplestreams index at
`https://cloud-images.ubuntu.com/releases/streams/v1/
com.ubuntu.cloud:released:download.json`. It contains every
release, every version, every architecture, every file variant
(`disk1.img`, `disk-kvm.img`, `root.tar.xz`, etc.), with sha256
checksums. This is the same feed MAAS and Juju consume, so it's
stable and well-maintained.

Filter logic:
1. Parse `products` map.
2. For each product where `arch == amd64`, find the newest
   version (by key — simplestreams version keys are YYYYMMDD
   date strings, lexicographic sort = chronological).
3. In that version, find the item whose key is `disk1.img`.
4. Extract release label, release codename, version date, `path`
   (appended to base mirror URL), sha256.
5. Sort all entries by version date desc.
6. Take the first 10.

Rejected: scraping `https://cloud-images.ubuntu.com/` HTML. The
simplestreams feed is tiny (~1–2 MB), versioned, and has
explicit checksums. HTML would need parsing + a separate fetch
for checksums.

Rejected: hardcoding a static list of URLs in the pmox binary.
Bitrots on every Ubuntu release, forces a pmox release to
support a new LTS.

Rejected: Ubuntu's sstream-mirror tool. Adds a non-Go dependency.

### D4. Snippets storage handling — prompt, don't create

The cleanest UX would be "if no snippets storage exists, pmox
creates one for you." We can't do that remotely: creating a
directory-type storage requires specifying a host filesystem
path (`/var/lib/vz/snippets` or similar), and pmox has no
authoritative way to know what paths exist on the PVE host or
which the admin has blessed.

Three-state handling:

1. **Already enabled**: if any storage reports `snippets` in its
   content list, pick the first such storage alphabetically and
   use it silently. No prompt.
2. **Dir-capable without snippets**: storage types `dir`, `nfs`,
   `cifs`, `cephfs`, `glusterfs` can hold snippets. If one
   exists but snippets isn't in its content list, prompt
   `enable snippets on <name>? [y/N]`. On `y`, call
   `UpdateStorageContent` with the current content list +
   `snippets` appended. On anything else, abort with
   `ExitConfig`.
3. **No dir-capable storage**: hard error with a message
   explaining that snippets storage is required and instructing
   the user to create one via the Proxmox UI. Don't try to
   guess host paths.

Rejected: auto-creating a dir storage at `/var/lib/vz/snippets`.
That path works on default-install PVE nodes but not on every
cluster, and the error mode (wrong path) is confusing.

Rejected: falling back to inline user-data via `ciuser` /
`sshkeys` / `runcmd` config keys. `cicustom` is the only way to
pass arbitrary cloud-config to PVE; there's no `runcmd=...`
config key.

### D5. VMID reservation — 9000–9099 convention

The Proxmox wiki's VMID convention reserves 9000–9099 for
templates. Using this range instead of `NextID` means:

- Templates cluster together in the VM list instead of being
  scattered in the regular 100+ range.
- `pmox launch` users can recognize template VMIDs at a glance.
- Multiple template builds don't collide with live workload
  VMIDs.

Allocation: `ListTemplates(ctx, node)` to get existing VM IDs,
filter to 9000–9099, pick the lowest unused slot. If the range
is full, error out naming the range — the user probably has 100
orphaned template-build VMs and should clean up.

Rejected: `NextID`. Works fine but puts templates at VMID 100,
101, 102 alongside regular VMs.

Rejected: hardcoding 9000 always. First build works, second
fails with "vmid exists" from PVE — confusing error surface.

Rejected: letting the user pick via a numeric prompt. One more
question in an already-long interactive flow.

### D6. Bake snippet — embedded, not templated

The cloud-init snippet is a static string with no Go
templating. It's constant across every build:

```yaml
#cloud-config
package_update: true
packages:
  - qemu-guest-agent
runcmd:
  - systemctl enable --now qemu-guest-agent
  - cloud-init clean --logs --seed
  - truncate -s 0 /etc/machine-id
  - rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_*
  - poweroff
```

Embedded via `//go:embed snippet.yaml` as a package-level
`[]byte`. Uploaded via `UploadSnippet` on every build with a
fixed filename (`pmox-qga-bake.yaml`) — overwriting any stale
copy from a previous run, so drift is impossible.

Rejected: Go text/template with per-build variables. There's
nothing to templatize. If a future slice wants per-image
customization (e.g. enabling `qemu-guest-agent` only on non-
Debian images), we template then.

Rejected: uploading a unique filename per build (e.g.
`pmox-qga-bake-<vmid>.yaml`). Leaves orphaned files on the
snippets storage after the template build completes. We can't
clean them up reliably from the API.

### D7. Download under the `import` content type

PVE 9 introduced a dedicated `import` storage content type for
disk images destined to become VM disks, and `import-from` on
`POST /qemu` will only read from a source volume whose storage
has `content=import` enabled — attempting to read from an
`iso`-content volume fails with `has wrong type 'iso' - needs to
be 'images' or 'import'`. PVE's installer enables `import` on
`local` by default on fresh PVE 9 installs.

So we pass `content=import` to `download-url` and reference the
file as `<storage>:import/<name>.qcow2` in the phase-8 `CreateVM`
call. The `.qcow2` extension is mandatory: PVE's storage plugin
import regex is `\.(ova|ovf|qcow2|raw|vmdk)` — `.img` (which is
what Canonical publishes upstream) is rejected by `download-url`
with `invalid filename or wrong extension`. Ubuntu cloud images
are qcow2 internally regardless of the upstream filename, so
renaming on the way in is safe; `qemu-img info` / the import
machinery read the actual format from the file header.

Rejected: downloading with `content=vztmpl` or `content=iso`.
Both are no longer accepted by `import-from` on PVE 9, and even
on PVE 8 the mental model gets worse, not better.

Rejected: downloading client-side (pmox does the HTTP GET and
uploads via the ISO upload endpoint). That's ~600 MB of
traffic through the pmox user's machine for every build. PVE
downloads directly from Canonical's mirror — much faster when
the PVE host has better bandwidth than the user.

### D8. Wait-stopped via GetStatus polling, not WaitTask

`Start` returns a PVE task UPID for the VM-start operation, but
that task finishes when the VM *begins* booting, not when the
VM has shut down. We need to wait for the guest OS to reach
`poweroff`. The only API signal for that is
`GET /nodes/{node}/qemu/{vmid}/status/current`, polling for
`status=stopped`.

- **Poll interval**: 5 seconds. The bake runs `apt-get update` +
  `apt-get install qemu-guest-agent` — realistically 60–180s on
  a reasonable uplink. 1-second polling wastes API calls; 10-s
  polling delays the user's feedback unnecessarily.
- **Default timeout**: 10 minutes. Overridable via `--wait`.
  Generous because `apt-get update` can be slow on small
  mirrors and package installs are the most variable step.
- **Detection**: `status=stopped` is authoritative. No need to
  cross-check with `qmpstatus` since the cloud-init `poweroff`
  hits both.

Rejected: using the guest agent to detect shutdown. The agent
stops responding before PVE reports `stopped`, leading to a
race where `AgentNetwork` fails but the VM is still running.

### D9. Post-bake cleanup — detach cloud-init drive

Before converting to template, we issue `SetConfig` with
`delete=ide2` to remove the cloud-init drive. Without this
step, every clone would inherit the cloud-init disk from the
template and cloud-init would see a stale seed on first boot.

PVE's clone path is smart enough to regenerate the cloud-init
drive from the clone's config keys — but only if the template
doesn't already have a cloud-init drive attached at clone time.

Rejected: leaving the cloud-init drive attached. Breaks the
clone-on-launch flow from slice 5.

Rejected: detaching before `Start`. The cloud-init drive is
what carries our bake snippet into the VM. Remove it too early
and the snippet never runs.

### D10. Non-interactive mode deferral

First cut is interactive-only. No `--yes`, no `--image`, no
`--target-storage`. Reasons:

- The UX decisions (which flag name? how to specify image by
  release codename vs version date? what happens when
  simplestreams moves a release?) are easier to make after we
  see the interactive flow in practice.
- `configure` sets a precedent: slice 2 shipped interactive-
  only, non-interactive flags came later as a follow-up.
- Non-interactive mode is a feature, not a fix. It doesn't
  belong in the same slice as the core flow.

A follow-up slice (call it `create-template-noninteractive`)
can add `--yes`, `--image <codename>`, `--target-storage <id>`,
`--snippets-storage <id>`, and `--bridge <iface>` — turning
the interactive prompts into flag reads when `--yes` is passed.
That slice is one file (`cmd/pmox/create_template.go`) plus a
handful of tests.

### D11. Error wrapping and exit codes

Every phase wraps errors with a phase-name prefix:

```go
return fmt.Errorf("check pve version: %w", err)
return fmt.Errorf("fetch ubuntu catalogue: %w", err)
return fmt.Errorf("pick target storage: %w", err)
return fmt.Errorf("enable snippets on %s: %w", name, err)
return fmt.Errorf("upload bake snippet: %w", err)
return fmt.Errorf("reserve vmid: %w", err)
return fmt.Errorf("download %s: %w", url, err)
return fmt.Errorf("create vm %d: %w", vmid, err)
return fmt.Errorf("start vm %d: %w", vmid, err)
return fmt.Errorf("wait for vm %d to stop: %w", vmid, err)
return fmt.Errorf("detach cloud-init drive from vm %d: %w", vmid, err)
return fmt.Errorf("convert vm %d to template: %w", vmid, err)
```

Exit code mapping uses the same `internal/exitcode.From` as
`pmox launch`, which already handles all the pveclient error
classes from slice 1/4.

### D12. Testing — fake PVE server + embedded simplestreams fixture

One integration test file, `internal/template/build_test.go`,
that:

1. Spins up a `httptest.Server` for the simplestreams feed,
   serving a trimmed-down fixture (one release, one version,
   one item — ~500 bytes of canned JSON in `testdata/`).
2. Spins up a second `httptest.Server` for the PVE API, same
   stateful-closure pattern as `internal/launch/launch_test.go`.
3. Walks the full state machine, asserting the exact sequence
   of endpoint hits.
4. Overrides the interactive pickers via an `Options` field
   (`pickImage func(...) int`) so the test can return a fixed
   choice without a TTY.

Separate unit tests:
- `simplestreams_test.go`: parsing + filter + sort against
  canned fixtures.
- `vmid_test.go`: allocator against canned `ListTemplates`
  results.
- `storage_test.go`: three-state snippets handling against
  canned `ListStorage` results, mocking the interactive prompt
  via an injected `confirm func(string) bool`.

Rejected: driving the full flow through the Cobra command. The
Cobra layer is tested in `cmd/pmox/create_template_test.go`
with flag parsing + exit code mapping only; orchestration is
tested at the `internal/template.Run` level.

## Risks / Trade-offs

- **[Risk] Simplestreams URL changes or Canonical restructures
  the feed]** → Mitigation: the feed has been stable for 10+
  years and is consumed by MAAS/Juju/cloud-init itself. If it
  breaks, we ship a point release with a fallback catalogue
  URL.

- **[Risk] Storage picked for `content=import` doesn't have that
  content type enabled]** → Mitigation: PVE 9's installer enables
  `import` on `local` by default. If the user picks a storage
  that doesn't have it, `download-url` fails fast with PVE's own
  error message. A follow-up could filter the picker list to
  import-capable storages up front.

- **[Risk] `import-from` semantics change across PVE point
  releases]** → Mitigation: pin the minimum version check at
  8.0 but exercise against PVE 8.x and 9.x manually before each
  pmox release. PVE 9 already renamed the accepted content type
  for `import-from` sources from `iso` to `import`.

- **[Risk] Apt install of qemu-guest-agent hangs on a slow
  mirror and blows the 10 minute wait budget]** → Mitigation:
  timeout error names the VMID and explains that the user can
  SSH in via the Proxmox console to see what happened, or bump
  `--wait`. No auto-retry.

- **[Risk] Bake snippet scrub step misses a Ubuntu-specific
  identifier and clones have colliding machine-ids]** →
  Mitigation: cloud-init regenerates machine-id on first boot
  when `/etc/machine-id` is empty, so truncation is the
  authoritative fix. SSH host keys are regenerated by
  `ssh-keygen --initial-install`-equivalent systemd units. If
  we ship a scrub miss, it surfaces as clone conflicts in
  real-world use and we add a scrub step in a patch.

- **[Risk] Enabling snippets on a dir storage breaks some other
  workload that depended on the current content list]** →
  Mitigation: we append, never replace. The existing content
  list is preserved verbatim. The prompt names the storage so
  the user can refuse if the storage is load-bearing for
  something else.

- **[Trade-off] Ubuntu-only means users on Debian-standard
  shops can't use this command]** → Intentional. Supporting
  Debian genericcloud is a follow-up slice. The decision to
  ship something useful for Ubuntu users now vs delaying for
  multi-distro scope is a deliberate prioritization call.

- **[Trade-off] Interactive-only means CI/automation flows
  can't use this command]** → Also intentional. Template builds
  are rare events (once per distro release, roughly), so
  interactive-first is the right default. Automation lands in
  the follow-up slice.
