## Why

`pmox list` shows name, VMID, node, status and IP. To see whether a VM is busy, how much memory it holds, or whether the host has room for another one, the user runs `pmox vm info` per VM and `pmox info` for the host. The cluster call `list` already makes returns each VM's CPU, memory, disk and uptime, and the host summary is one more request, so `list` can answer "what is going on" by itself.

## What Changes

- The table gains CPU, MEM, DISK and UP columns. Running VMs show usage against allocation; stopped VMs show the allocation only.
- STATUS becomes STATE with a coloured dot on a terminal (`● running`, `○ stopped`).
- Rows sort running first, then by name.
- **BREAKING** VMID leaves the default table and NODE shows only on a multi-node cluster. `--wide` shows both.
- **BREAKING** The overlay suffix in the IP column shortens from ` (tailscale)` / ` (netbird)` to ` ts` / ` nb`.
- On a terminal, a host line per node goes above the table (uptime, CPU, memory, largest storage pool) and a count line below it, with a pointer to `--all` when unmanaged VMs exist. Piped output carries the table alone.
- On a terminal narrower than the table, columns drop in a fixed order instead of wrapping.
- New `pmox list --watch` (`-w`) redraws the same view every `--interval` (default 5s) until `q` or Ctrl-C.
- `-o json` keeps its array shape and gains `cpus`, `cpu_used`, `mem_bytes`, `max_mem_bytes`, `disk_bytes` and `uptime_seconds` per VM.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `list-info-lifecycle`: `pmox list` columns, ordering, overlay marker, host line, footer, narrow-terminal behaviour, `--wide` and `--watch`.

## Impact

- `internal/pveclient/cluster.go`: `Resource` reads the usage fields the API already sends.
- `internal/vm/table.go`, `cmd/pmox/list.go`: new renderer, flags and watch loop; host line reuses the node and storage collection in `cmd/pmox/hostinfo.go`.
- Scripts that parse the text table break (column set, order of rows, overlay suffix). Scripts on `-o json` are unaffected.
- README.md, llms.txt, docs/remote-access.md describe the old table and overlay suffix.
- No new dependencies; the watch view uses bubbletea, already in use.
