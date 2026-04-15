## D1. README structure — mirrors tack

Section order (copied from `tackhq/tack`'s README, adapted for
pmox):

1. One-paragraph pitch + a single-line install command
2. Install — Homebrew tap, `go install`, raw binary download
3. PVE-side setup (link to `docs/pve-setup.md` for the long version)
4. Quick start — 5 lines: `pmox configure`, `pmox launch web1`,
   `ssh pmox@<ip>`, `pmox delete web1`
5. Commands reference — table with columns `Command | Summary | Example`
6. Cloud-init — leads with a complete working snippet
7. Post-create hooks — leads with a `--post-create ./script.sh` example
8. Configuration — where `config.yaml` lives, its schema, keychain
9. Environment variables — `PMOX_SERVER`, `PMOX_TEMPLATE`, etc.
10. Exit codes — table `0 ok | 2 config | 3 auth | 4 remote | 5 timeout | 6 generic | 7 hook`
11. Troubleshooting — 5–8 common failures with diagnoses
12. Development — `make build test lint release-dry-run`
13. License

No table of contents at the top — GitHub renders one from headings.
No badges row — tack doesn't have one.

## D2. llms.txt shape

Reference: `https://llmstxt.org/` — one markdown file, flat
structure, grep-friendly.

```
# pmox

> A multipass-style CLI for launching ephemeral VMs on a remote
> Proxmox VE cluster via the PVE HTTP API. Single static Go
> binary; does not run on the Proxmox host.

## Commands

- `pmox configure`: interactive setup...
- `pmox launch <name>`: clone a template, push cloud-init, start...
- ...

## Flags

Persistent root flags: `--server`, `--debug`, `--verbose`,
`--no-color`, `--output <text|json>`.

Launch-specific: `--cpu`, `--mem`, `--disk`, ...

## Exit codes

| Code | Name | Meaning |
...

## Config file

Location: `$XDG_CONFIG_HOME/pmox/config.yaml`
Schema: ...
Secrets: stored in system keychain via go-keyring

## Examples

- examples/cloud-init.yaml
- examples/post-create.sh
- examples/tack.yaml
- examples/ansible/playbook.yaml

## Links

- README: https://github.com/eugenetaranov/pmox/blob/main/README.md
- tack (companion project): https://github.com/tackhq/tack
```

Hard constraint: the file is ≤ 15 KB so it fits inside a typical
LLM context prefix without eating the whole window.

## D3. Examples — runnable, not pedagogical

Every file in `examples/` must actually work against a default
Ubuntu 24.04 template. No placeholders beyond `ssh_authorized_keys`
(which the user must edit before use — documented in a header
comment).

**`examples/cloud-init.yaml`** (already shipped by slice 7 if
that slice placed it here; otherwise move it).

**`examples/post-create.sh`**:
```sh
#!/bin/sh
set -euo pipefail
: "${PMOX_IP:?PMOX_IP not set}"
: "${PMOX_USER:=pmox}"
echo "waiting for cloud-init on $PMOX_IP"
ssh -o StrictHostKeyChecking=no "$PMOX_USER@$PMOX_IP" \
    'cloud-init status --wait'
echo "cloud-init done"
```

**`examples/tack.yaml`** — a minimal tack config installing `htop`
via the `apt` module. Exact shape depends on tack's config
format; reference `github.com/tackhq/tack/examples/` and mirror
the simplest one.

**`examples/ansible/playbook.yaml`**:
```yaml
---
- name: provision
  hosts: all
  become: true
  tasks:
    - name: install htop
      ansible.builtin.apt:
        name: htop
        state: present
        update_cache: true
```

**`examples/README.md`** — an index: one paragraph per file
explaining what it demonstrates and the `pmox launch` command
that uses it.

## D4. docs/pve-setup.md

One page covering:
1. API token creation (`pveum user token add ...` with exact scopes)
2. Required permissions — `PVEVMAdmin` on `/vms` and
   `Datastore.AllocateSpace` on the target storage
3. Template preparation — the three lines users forget:
   `apt install qemu-guest-agent`, `agent: 1` in the template
   config, cloud-init drive attached
4. Common first-launch errors: `403 Forbidden` → permission
   missing, `qemu-guest-agent not responding` → template issue,
   `storage does not have 'snippets' in its content types` →
   D-T2 error, link back to cloud-init section

The README's PVE-side-setup section is a 5-line summary that
links here for details.

## D5. Link checker

Prefer `lychee` if installed; fall back to a 30-line Go helper
under `internal/tools/doccheck/` that walks `README.md`,
`llms.txt`, `docs/*.md`, and `examples/README.md`, extracts every
relative link (`./docs/pve-setup.md`, `examples/cloud-init.yaml`),
and asserts each target file exists.

`make docs-check`:
```make
docs-check:
    @if command -v lychee >/dev/null; then \
        lychee --offline README.md llms.txt docs/ examples/; \
    else \
        go run ./internal/tools/doccheck; \
    fi
```

Offline mode only — no network fetches. We're validating
internal references, not upstream URLs (those rot too fast to
be worth CI's time).

CI job:
```yaml
docs:
  if: contains(github.event.pull_request.changed_files, 'README.md')
      || contains(github.event.pull_request.changed_files, 'llms.txt')
      || contains(github.event.pull_request.changed_files, 'docs/')
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - run: make docs-check
```

## D6. What the README does NOT cover

- Internal architecture (slice boundaries, the state machine,
  the pveclient contract). That lives in `openspec/` and is for
  maintainers.
- LXC, snapshots, multi-VM launch, shell/exec, host mounts, or
  Windows. All out of scope per ROADMAP.md.
- The `openspec/` workflow itself. Contributors learn that from
  tack's docs or from OpenSpec upstream.

The README is a *user* document. Anything a non-contributor
doesn't need is out.

## D7. Tone

Same tone as tack's README: declarative, short paragraphs, no
marketing language, no emoji, no "awesome" or "blazing". The
pitch paragraph is one sentence explaining the what and one
sentence explaining why you'd use it over the PVE UI or
Terraform.

Example opening:

> pmox is a single static Go binary that launches and manages
> ephemeral VMs on a remote Proxmox VE cluster via the PVE HTTP
> API. It's a multipass-style CLI for homelabs and dev clusters
> where Terraform is too much and the web UI is too slow.
