## MODIFIED Requirements

### Requirement: `pmox list` command

The CLI SHALL expose `pmox list` which enumerates VMs on the
resolved cluster. By default it shows only VMs tagged `pmox`. The table SHALL have the columns NAME, STATE, CPU, MEM, DISK, UP and IP. A running VM SHALL show CPU use against its cores, memory use against its allocation, its disk size and its uptime; a stopped VM SHALL show its allocation with a dash for use and uptime. `--wide` SHALL add VMID and NODE. NODE SHALL also be shown, without `--wide`, when the cluster has more than one node. JSON output SHALL stay an array with one object per VM.

#### Scenario: Default list shows only pmox-tagged VMs
- **WHEN** `pmox list` is invoked against a fake cluster with 3 VMs, 2 tagged `pmox` and 1 untagged
- **THEN** the output table SHALL contain 2 rows
- **AND** the untagged VM SHALL NOT appear

#### Scenario: `--all` shows every VM
- **WHEN** `pmox list --all` is invoked against the same cluster
- **THEN** the output table SHALL contain 3 rows

#### Scenario: Table columns are fixed
- **WHEN** `pmox list` prints output in table mode on a single-node cluster
- **THEN** the header row SHALL be `NAME STATE CPU MEM DISK UP IP`
- **AND** each data row SHALL have those seven columns in that order

#### Scenario: JSON output
- **WHEN** `pmox list --output json` is invoked
- **THEN** stdout SHALL be a JSON array of objects
- **AND** each object SHALL have keys `name`, `vmid`, `node`, `status`, `ip`, `cpus`, `cpu_used`, `mem_bytes`, `max_mem_bytes`, `disk_bytes`, `uptime_seconds`

#### Scenario: IP column is blank for stopped VMs
- **WHEN** a VM in the table has status `stopped`
- **THEN** the IP cell SHALL be rendered as a dash in table mode
- **AND** the `ip` field SHALL be an empty string in JSON mode
- **AND** the launcher SHALL NOT call `AgentNetwork` for that VM

#### Scenario: Running VM row
- **WHEN** a running VM has 4 cores at 4% use, 2.1 of 8 GiB memory, a 40 GiB disk and 3 days 4 hours uptime
- **THEN** its cells SHALL read `4% of 4`, `2.1/8.0 GiB`, `40G` and `3d 4h`

#### Scenario: Stopped VM row
- **WHEN** a stopped VM is allocated 2 cores, 4 GiB memory and a 20 GiB disk
- **THEN** its CPU and MEM cells SHALL show a dash for use with `of 2` and `/4.0 GiB`, DISK SHALL read `20G` and UP SHALL be a dash

#### Scenario: Wide
- **WHEN** a user runs `pmox list --wide` on a single-node cluster
- **THEN** the header row SHALL include VMID and NODE

#### Scenario: Multi-node cluster
- **WHEN** the cluster has nodes `p0` and `p1` and a user runs `pmox list`
- **THEN** the table SHALL include the NODE column

### Requirement: list marks overlay addresses

When the address `pmox list` shows for a VM is on a `tailscale*` interface, the table SHALL suffix it with ` ts`; on a `wt*` interface, with ` nb`. JSON output SHALL keep the plain address in `ip` and SHALL add `"overlay": "tailscale"` or `"overlay": "netbird"`.

#### Scenario: Overlay VM in the table
- **WHEN** `address: overlay` is set and `payzoff` reports `tailscale0` 100.104.54.92
- **THEN** its IP cell SHALL read `100.104.54.92 ts`

#### Scenario: LAN address unmarked
- **WHEN** the address shown is on `ens18`
- **THEN** the cell SHALL show the address alone

### Requirement: list columns stay aligned
`pmox list` SHALL size each column to its widest cell, including an overlay marker after an address and the state glyph, so that every row's cells start in the same column. Colour codes SHALL NOT count toward a cell's width.

#### Scenario: Overlay label in the IP column
- **WHEN** one VM shows `192.168.0.25` and another `100.104.54.92 ts`
- **THEN** the IP column is as wide as the longer value and no row has trailing text outside a column

#### Scenario: Coloured state
- **WHEN** the table is printed in colour with one running and one stopped VM
- **THEN** the CPU cells of both rows start in the same column

## ADDED Requirements

### Requirement: list orders running VMs first
`pmox list` SHALL list running VMs before all others and SHALL order by name within each group, in table and JSON output.

#### Scenario: Mixed states
- **WHEN** `alpha` is stopped, `zeta` and `beta` are running
- **THEN** the rows are `beta`, `zeta`, `alpha`

### Requirement: list shows state at a glance on a terminal
When stdout is a terminal, the STATE cell SHALL start with a glyph: `●` for running, `○` for stopped and `◐` for any other state, coloured green, dim and yellow. With `--no-color` or `NO_COLOR` the glyph SHALL stay and the colour SHALL be dropped. When stdout is not a terminal the cell SHALL hold the state word alone.

#### Scenario: Piped
- **WHEN** a user runs `pmox list | cat`
- **THEN** the output holds no glyph and no escape sequence

#### Scenario: No colour
- **WHEN** a user runs `pmox list --no-color` on a terminal
- **THEN** state cells read `● running` and `○ stopped` with no escape sequence

### Requirement: list shows a host line above the table
When stdout is a terminal and the output mode is the table, `pmox list` SHALL print, above the table, one line per online node with its name, uptime, CPU use and core count, memory use and total, and the free space of its largest storage pool. The first line SHALL start with the context's name. A failure to read node or storage data SHALL omit the affected part and SHALL NOT fail the command.

#### Scenario: Single node
- **WHEN** node `p0` is up 10 days with 16 cores at 2%, 27.7 of 62.5 GiB memory and largest pool `vm-data` with 1553 GiB free
- **THEN** the first line names the context and shows those figures for `p0`

#### Scenario: Node data unavailable
- **WHEN** the node list call fails and the VM call succeeds
- **THEN** the table and count line are printed, no host line is printed, and the exit status is 0

### Requirement: list shows a count line below the table
When stdout is a terminal and the output mode is the table, `pmox list` SHALL print, below the table, the number of listed VMs running and stopped. Without `--all`, when the cluster holds VMs that are not pmox-managed, the line SHALL say how many and name `--all`.

#### Scenario: Count line
- **WHEN** 2 pmox VMs run, 4 are stopped and 6 other VMs exist
- **THEN** the last line reads `2 running, 4 stopped` and says 6 more VMs are shown by `--all`

#### Scenario: Nothing unmanaged
- **WHEN** every VM on the cluster is pmox-managed
- **THEN** the count line does not mention `--all`

### Requirement: piped list output is the table alone
When stdout is not a terminal, `pmox list` SHALL print neither the host line nor the count line.

#### Scenario: Piped output is the table alone
- **WHEN** a user runs `pmox list | wc -l` with 6 pmox VMs
- **THEN** the count is 7

### Requirement: list fits the terminal
When stdout is a terminal narrower than the table, `pmox list` SHALL drop columns in the order DISK, UP, MEM, CPU until the table fits, and SHALL always keep NAME, STATE and IP. Output that is not a terminal SHALL keep every column.

#### Scenario: 80 columns
- **WHEN** the full table is 86 columns wide and the terminal is 80
- **THEN** the table is printed without DISK and no row wraps

#### Scenario: Piped from a narrow terminal
- **WHEN** the same command is piped to a file
- **THEN** the file holds all seven columns

### Requirement: list --watch refreshes in place
`pmox list --watch` (`-w`) SHALL show the same view as `pmox list` and redraw it every `--interval` (default 5s, minimum 1s) until the user presses `q`, Esc or Ctrl-C, then exit 0 and restore the terminal. It SHALL honour `--all` and `--wide`. A refresh that fails SHALL keep the last view on screen, show the error, and try again at the next interval. `--watch` when stdout is not a terminal, or with `-o json`, SHALL be a usage error. `--interval` without `--watch` SHALL be a usage error.

#### Scenario: State change appears
- **WHEN** `pmox list --watch` is open and `web1` is started elsewhere
- **THEN** within one interval `web1` shows as running and moves into the running group

#### Scenario: Quit
- **WHEN** the user presses `q`
- **THEN** pmox exits 0 and the shell prompt returns on the normal screen

#### Scenario: Transient failure
- **WHEN** one refresh fails and the next succeeds
- **THEN** the table stays visible throughout, the error is shown after the failure and is gone after the success

#### Scenario: Piped
- **WHEN** a user runs `pmox list --watch | cat`
- **THEN** pmox exits with a usage error and prints no table

#### Scenario: Interval too short
- **WHEN** a user runs `pmox list --watch --interval 100ms`
- **THEN** pmox exits with a usage error naming the 1s minimum
