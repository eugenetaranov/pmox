## D1. Six commands, one shared resolver

Every command in this slice takes a `<name|vmid>` argument (except
`list`). Rather than re-implementing the lookup in six files, put
it in `internal/vm/resolve.go`:

```go
type Ref struct {
    VMID int
    Node string
    Name string
}

func Resolve(ctx context.Context, c *pveclient.Client, arg string) (*Ref, error)
```

- If `arg` parses as int: single GET for status via `ClusterResources`
  filtered to the VMID, return the matching `Ref`. Error if not
  found.
- Otherwise: fetch all VM-type cluster resources, filter by
  `name == arg`, return the one match. Error "not found" on zero
  matches, error "multiple VMs named %q: vmids %v — pass the VMID
  instead" on two or more.

One round trip per command. Acceptable — these are human-speed
commands, not a hot loop.

## D2. `list` — table renderer, not a library

The list command prints a fixed five-column table. No
`tablewriter`/`text/tabwriter` dependency — hand-rolled with
`fmt.Fprintf(w, "%-20s %-6s ...", ...)`, widths computed from the
longest row. Same pattern tack uses for its own list commands.

```
NAME                 VMID   NODE       STATUS   IP
web1                 104    pve1       running  192.168.1.43
db1                  105    pve1       stopped  -
```

Width columns:
- NAME: `max(len(name), 4)` capped at 40
- VMID: 6
- NODE: `max(len(node), 4)` capped at 20
- STATUS: 8
- IP: 15 (fits IPv4; IPv6 truncates)

`--output json` emits `[{"name":..., "vmid":..., "node":..., "status":..., "ip":...}]`.

**IP population**: for each running VM in the table, call
`AgentNetwork` in parallel with a small `errgroup` (bounded to 8
concurrent calls). Skip VMs that aren't running. If `AgentNetwork`
errors (agent not up), IP is blank. Don't block the whole list on
one slow agent.

**`--all`**: drops the `tags=pmox` filter. By default, `list` calls
`ClusterResources(ctx, "vm")` and filters client-side to entries
whose `Tags` string contains `pmox`. Tag filtering is client-side
because PVE's `cluster/resources` endpoint doesn't support
server-side tag filtering.

## D3. `info` — one VM, full detail

`pmox info web1` fetches:

1. `ClusterResources` — to get node + vmid from the name
2. `GetStatus(node, vmid)` — uptime, cpu load, mem use
3. `GetConfig(node, vmid)` — NEW endpoint, `GET /nodes/{node}/qemu/{vmid}/config`, returns the full VM config map
4. `AgentNetwork(node, vmid)` — if running, all IPs (not just the picked one)

Renders as:

```
Name:     web1
VMID:     104
Node:     pve1
Status:   running (up 3h 12m)
Tags:     pmox
Template: ubuntu-24.04 (vmid 9000)
CPU:      2 cores
Memory:   2048 MB
Disk:     scsi0 20G on local-lvm
Network:  eth0 192.168.1.43/24  a8:a1:59:...
          eth0 fe80::aab1:59ff:.../64
```

`GetConfig` is a new client method. Keeping it separate from
`GetStatus` because PVE splits them across two endpoints and the
shapes are wildly different.

## D4. `start` / `stop` / graceful-vs-force

Three status endpoints on PVE:
- `POST /status/start` — start stopped VM
- `POST /status/shutdown` — ACPI graceful shutdown
- `POST /status/stop` — hard power off

`pmox stop web1` defaults to shutdown. `pmox stop --force web1`
sends stop. The distinction matters — ACPI shutdown can hang
forever if the guest OS is broken or there's no init system
responding; stop is always immediate.

Both return UPIDs; both go through `WaitTask`. `--no-wait` short-
circuits the wait.

New client methods:

```go
func (c *Client) Shutdown(ctx context.Context, node string, vmid int) (string, error) // UPID
func (c *Client) Stop(ctx context.Context, node string, vmid int) (string, error)     // UPID
```

Mechanically identical to the existing `Start` — same shape, same
UPID return, same parse helper.

## D5. `delete` — the interrupt case

The delete flow is three calls:

```
1. GetStatus    — is it running?
2. Shutdown/Stop — if running
3. WaitTask     — if step 2 issued
4. Delete       — DELETE /nodes/{node}/qemu/{vmid}
5. WaitTask     — wait for destroy
```

If the user Ctrl-Cs between 3 and 4, the VM is stopped but not
deleted. It's still tagged `pmox`. Re-running `pmox delete web1`
will succeed: step 1 reports `stopped`, steps 2–3 are skipped,
step 4 runs.

If the user Ctrl-Cs between 1 and 2 (before shutdown is issued),
nothing happened. Next run starts over.

If the user Ctrl-Cs between 4 and 5, the destroy task is already
queued on PVE and will finish on its own. Re-running `pmox delete`
will get `ErrNotFound` from `GetStatus`, which we treat as success
with a "already gone" stderr note.

```go
if errors.Is(err, pveclient.ErrNotFound) {
    fmt.Fprintln(stderr, "VM already deleted; nothing to do")
    return nil
}
```

**The `pmox` tag gate**: before calling Shutdown or Delete,
check that `status.Tags` contains `pmox`. If not:

```
refusing to delete VM 104 (web1): not tagged "pmox".
pass --force to delete anyway.
```

`--force` bypasses the gate. This is the parked-thread fix: users
can safely run `pmox delete` in a shared cluster without worrying
about nuking hand-managed VMs.

## D6. `clone` — reuse the launch state machine

`pmox clone web1 web1-copy` is conceptually "launch, but the template
is an existing VM". Implementation: import `internal/launch` and
construct `launch.Options` with `TemplateID` set to the source
VM's VMID. The launch state machine does the rest.

```go
// cmd/pmox/clone.go
ref, _ := vm.Resolve(ctx, client, srcArg)
opts := launch.Options{
    Client:     client,
    Node:       ref.Node,
    Name:       newName,
    TemplateID: ref.VMID,
    CPU:        cpuFlag,
    // ... same resolution logic as launch ...
}
_, err := launch.Run(ctx, opts)
```

**Caveat**: PVE's `Clone` endpoint works on both templates and
regular VMs. The clone target must be *stopped* for a linked clone,
or running is fine for a full clone. This slice forces `full=1`
(same as slice 5), so the source can be running.

**Flag set**: `clone` accepts the same `--cpu`, `--mem`, `--disk`,
`--ssh-key`, `--user`, `--wait`, `--no-wait-ssh` as `launch`. It
does *not* accept `--template` (the source VM argument is the
template) or `--cloud-init` (slice 7).

## D7. Name resolution — when is duplicate OK?

PVE allows multiple VMs to have the same name. Pmox doesn't prevent
that (you can `pmox launch web1` twice). When a user later says
`pmox stop web1` and there are two, we must refuse:

```
multiple VMs named "web1": vmids 104, 107 — pass the VMID instead
```

Exit `ExitConfig`. The user then runs `pmox stop 104` or
`pmox stop 107`.

**Rejected:** auto-picking the first/newest/tagged one. Silent
ambiguity is worse than an explicit error.

**Rejected:** making names unique at launch time. Would require
an index check against `ClusterResources` on every launch, which
races against other pmox invocations. Cheaper to fail late, when
it actually matters, with a message that tells the user exactly
how to fix it.

## D8. Parallel IP fetch in `list`

```go
import "golang.org/x/sync/errgroup"

g, gctx := errgroup.WithContext(ctx)
g.SetLimit(8)
ips := make([]string, len(rows))
for i, row := range rows {
    i, row := i, row
    if row.Status != "running" { continue }
    g.Go(func() error {
        ifaces, err := client.AgentNetwork(gctx, row.Node, row.VMID)
        if err != nil { return nil } // swallow; IP stays blank
        ips[i] = pickIPv4(ifaces) // reuse launch.pickIPv4 — export it
    })
}
g.Wait()
```

**`pickIPv4` reuse**: slice 5 defined this as unexported. Export
it as `launch.PickIPv4` (capitalized) so `list` can call it. The
D-T3 heuristic is the same contract — same skip list, same
ordering. One implementation, one test file.

**`errgroup`**: already in tack's go.mod presumably; add
`golang.org/x/sync/errgroup` to pmox if not already transitively
present.

## D9. Shutdown timeout

`pmox stop web1` without `--no-wait` calls `WaitTask` on the
shutdown UPID. PVE's shutdown task polls the guest for up to
`shutdownTimeout` seconds (default 180s on PVE 8) before giving
up with an error. We surface that error unchanged — the message
is informative ("guest did not respond to ACPI shutdown request").

If the user wants faster, they pass `--force`. We don't add a
`--shutdown-timeout` flag — PVE's default is fine and configurable
in the PVE UI anyway.

## D10. Testing — reuse the fake PVE server

`internal/vm/resolve_test.go` gets table tests against a fake
`httptest.Server` serving canned `cluster/resources` responses:
- single match by name
- single match by VMID
- multiple matches by name → error
- no matches → error

Each command in `cmd/pmox/*.go` gets an end-to-end test using the
same fake-PVE harness from slice 5 (extract it to
`internal/pvetest/fake.go` so both packages can import it — this
is a refactor of slice 5's test helper, not a new dependency).

`list_test.go` asserts the rendered table matches a golden file
for a 3-VM fixture, and asserts `--output json` emits valid JSON
parseable back to the same shape.
