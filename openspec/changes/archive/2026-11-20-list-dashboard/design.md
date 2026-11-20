## Context

`pmox list` makes one `cluster/resources` call, then one guest-agent call per running VM for its IP, and prints five columns. The same `cluster/resources` rows carry `cpu`, `maxcpu`, `mem`, `maxmem`, `maxdisk` and `uptime`, which pmox drops today. `pmox info` already collects per-node uptime, CPU, memory and storage pools in `collectHostInfo`.

## Goals / Non-Goals

**Goals:**
- One command that shows VM state, load and host headroom.
- No slower than today in the common case.
- Piped and JSON output stay machine-friendly.

**Non-Goals:**
- A mount/sync column (needs local mount state; separate change).
- Acting on VMs from the watch view (start/stop keys). It is read-only.
- Changing the VM picker's rows.

## Decisions

**D1. Default columns: marker, NAME, STATE, CPU, MEM, DISK, UP, IP.**
VMID is rarely typed since names and completion work everywhere, and NODE is constant on a single-node cluster, so both move to `--wide`. NODE comes back by itself when the cluster has more than one node. Alternative: keep all nine columns. Rejected: about 105 columns wide, which wraps on a normal terminal.

**D2. Cell formats.**
CPU `4% of 4` (running) / `– of 4` (stopped). MEM `2.1/8.0 GiB` / `–/8.0 GiB`. DISK is the allocated size, `40G`. UP reuses `uptimeStr` from host info. A dash is `–` on a terminal and `-` when piped, matching the existing blank-IP cell for piped output.

**D3. Host line and footer only on a terminal.**
They are context for a person. Keeping them out of piped output means `pmox list | grep running` and `| wc -l` still see a header row and one row per VM. The host line costs `ListNodes` plus `ListStorage` per online node; these run concurrently with the IP lookups, so wall time is still bounded by the slowest agent call. If either fails, the line (or its storage part) is omitted and `list` still succeeds: the table is the point.

**D4. Storage shown is the pool with the largest total size on that node.**
One pool fits on the line; the largest is where VM disks live in practice. `pmox info` remains the full view.

**D5. Narrow terminals drop columns in the order DISK, UP, MEM, CPU.**
Wrapped rows are unreadable; the dropped columns are the least needed to pick a VM. NAME, STATE and IP always stay. Piped output never drops columns.

**D6. Colour and glyph.**
On a terminal the STATE cell is prefixed with `●` (running, green), `○` (stopped, dim) or `◐` (anything else, yellow). `--no-color` / `NO_COLOR` remove colour and keep the glyph, whose shape already differs. Piped output has the word alone.

**D7. Overlay marker ` ts` / ` nb`.**
The long form made IP the widest column. JSON `overlay` is unchanged.

**D8. `--watch` is a bool with a separate `--interval`.**
An optional-value flag (`--watch=2s`) needs the `NoOptDefVal` handling that already caused confusing help once. The view is a bubbletea program on the alternate screen that reuses the table renderer; `q`, Esc and Ctrl-C quit. Each tick re-reads `cluster/resources` and the nodes. IPs are cached across ticks and looked up again only for a VM that has no address yet or whose state changed, so a tick is two or three API calls, not one per VM. Minimum interval 1s. A failed tick keeps the last good frame and shows the error on the footer line; the next tick retries. `--watch` without a terminal, or with `-o json`, is a usage error.

**D9. JSON adds fields, keeps the array.**
Wrapping the array in an object with a host block would break every existing consumer for data `pmox info -o json` already provides.

**D10. Order: running first, then name, in both table and JSON.**

## Risks / Trade-offs

- Scripts parsing the table break → noted as BREAKING in the release notes; `-o json` is the stable interface and only grows.
- PVE reports guest memory from the host side when the balloon driver is absent, so MEM can read near the allocation → shown as reported; no correction attempted.
- Two more API calls per `list` on a terminal → run in parallel with IP lookups; failures are non-fatal.
- `default-vm` spec says the default row carries `*`; unchanged, the marker column stays first.
