## D1. Package layout — `cmd/pmox/launch.go` + `internal/launch`

`launch` is complex enough to split. The Cobra command lives in
`cmd/pmox/launch.go` (flag wiring, flag→options translation, exit
code mapping), and the orchestration logic lives in
`internal/launch` so it's testable without a Cobra root.

```
cmd/pmox/launch.go       // Cobra command + flag parsing
internal/launch/launch.go  // Run(ctx, Options) error — state machine
internal/launch/cloudinit.go // built-in user-data renderer
internal/launch/ip.go      // AgentNetwork polling + IP picker (D-T3)
internal/launch/ssh.go     // TCP+SSH handshake wait
```

Rejected: one monolithic `launch.go` under `cmd/pmox/`. That's the
"trivial command" shape and launch isn't trivial — it has five
discrete phases each worth its own file and test.

## D2. The state machine is a linear function, not an FSM type

`internal/launch.Run(ctx, opts) error` is one top-to-bottom function
with nine labelled phases. No goroutines, no channels, no FSM struct
with transitions. Each phase is 5–20 lines and terminates on first
error.

```go
func Run(ctx context.Context, opts Options) error {
    // 1. nextid
    vmid, err := opts.Client.NextID(ctx)
    if err != nil { return fmt.Errorf("allocate vmid: %w", err) }

    // 2. clone
    upid, err := opts.Client.Clone(ctx, opts.Node, opts.TemplateID, vmid, opts.Name)
    if err != nil { return fmt.Errorf("clone template: %w", err) }
    if err := opts.Client.WaitTask(ctx, opts.Node, upid, 120*time.Second); err != nil { ... }

    // 3. tag — BEFORE resize, per D-T1
    if err := opts.Client.SetConfig(ctx, opts.Node, vmid, map[string]string{"tags": "pmox"}); err != nil {
        return fmt.Errorf("tag vm %d: %w (vm exists on cluster, run pmox delete)", vmid, err)
    }

    // 4. resize
    // 5. config (memory, cores, cloud-init, sshkeys, agent=1)
    // 6. start + wait task
    // 7. wait-IP (agent poll loop)
    // 8. wait-SSH (unless --no-wait-ssh)
    // 9. print IP + VMID on stdout
}
```

**Rejected:** a state-machine struct with explicit transitions. The
flow is linear — there are no branches, no retries (except the agent
poll), no choice points. An FSM type would be a class with one
method called sequentially. Just write the sequence.

## D3. D-T1 tag order — tag before resize, error message

The tag step comes *immediately* after the clone task completes.
If tagging fails, the error message must tell the user the VM still
exists and is cleanable:

```
tag vm 104: <underlying error> (vm exists on cluster, run pmox delete 104)
```

Every later phase's failure gets the same suffix. We're not adding
cleanup code — D-T1 explicitly rejected auto-rollback. The error
message *is* the cleanup UX.

## D4. Built-in cloud-init — template, not a struct

Built-in user-data is a single Go text/template embedded in
`cloudinit.go`:

```yaml
#cloud-config
users:
  - name: {{.User}}
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - {{.SSHKey}}
ssh_pwauth: false
package_update: true
packages:
  - qemu-guest-agent
runcmd:
  - systemctl enable --now qemu-guest-agent
```

Rendered into a string, then passed to `SetConfig` as
`cicustom=user=<storage>:snippets/<vmid>-user-data.yaml` — wait,
no. D-T5 says built-in cloud-init is pushed via the `ciuser` /
`sshkeys` config keys directly, not via a snippets upload. The
snippets path is only for `--cloud-init` in slice 7.

**Revised:** built-in mode sets `ciuser`, `sshkeys`, `ipconfig0=ip=dhcp`,
and `agent=1` via `SetConfig`. No YAML template rendered, no snippets
storage touched. PVE's cloud-init layer expands those keys into a
working user-data on boot.

```go
kv := map[string]string{
    "name":      opts.Name,
    "memory":    strconv.Itoa(opts.MemMB),
    "cores":     strconv.Itoa(opts.CPU),
    "agent":     "1",
    "ciuser":    opts.User,
    "sshkeys":   opts.SSHPubKey,  // SetConfig double-encodes this
    "ipconfig0": "ip=dhcp",
}
opts.Client.SetConfig(ctx, node, vmid, kv)
```

`cloudinit.go` becomes thin: a single function that builds this map
from `Options`. The text/template approach moves to slice 7 where
it actually matters.

## D5. IP discovery — poll loop with the D-T3 picker

```go
// ip.go
func WaitForIP(ctx context.Context, c *pveclient.Client, node string, vmid int, timeout time.Duration) (string, error) {
    deadline := time.Now().Add(timeout)
    for {
        ifaces, err := c.AgentNetwork(ctx, node, vmid)
        if err == nil {
            if ip := pickIPv4(ifaces); ip != "" { return ip, nil }
        }
        // agent not up yet: err is ErrAPIError with "guest agent is not running".
        // don't distinguish — any non-nil error or empty pick means retry.
        if time.Now().After(deadline) {
            return "", fmt.Errorf("qemu-guest-agent not responding on VM %d; install qemu-guest-agent in your template and re-run launch", vmid)
        }
        select {
        case <-ctx.Done(): return "", ctx.Err()
        case <-time.After(1 * time.Second):
        }
    }
}

func pickIPv4(ifaces []pveclient.AgentIface) string {
    skipPrefixes := []string{"lo", "docker", "br-", "veth", "cni", "virbr", "tun"}
    // ... D-T3 heuristic ...
}
```

**Poll interval**: 1 second. The agent takes 20–60s to come up on a
typical ubuntu template. 500ms would waste 60 API round-trips;
1s waste 30. Not tunable.

**Default timeout**: 180s. Overridable via `--wait`. Slower than
`WaitTask`'s 60–120s because we're waiting on a guest service, not
a PVE API task.

**IP picker** is a pure function over `[]AgentIface`, unit-tested
with table cases covering:
- single eth0 with one IPv4 → return it
- eth0 + docker0 → skip docker0, return eth0's IPv4
- eth0 with IPv6 only → fall through to "no usable IPv4"
- only link-local (169.254) → fall through
- the fallback path (step 3 of D-T3)

## D6. SSH wait — handshake only, no shell

```go
// ssh.go
func WaitForSSH(ctx context.Context, ip string, timeout time.Duration) error {
    // dial TCP :22 with exponential backoff until success or timeout
    // then run an ssh handshake with ssh.ClientConfig.HostKeyCallback = InsecureIgnoreHostKey
    //   no auth, just banner exchange — we're not logging in
}
```

**Why a full handshake and not just TCP dial:** a TCP connect to `:22`
succeeds as soon as sshd binds the port, but sshd may still be
generating host keys and will immediately close the connection. A
handshake proves sshd is actually ready to serve. The handshake
fails with "no authentication method" after banner exchange — that
counts as success for us because it means sshd responded.

**`--no-wait-ssh`**: skips this phase entirely. For templates where
SSH isn't used or where the user wants faster return.

**Dependency**: this adds `golang.org/x/crypto/ssh` to `go.mod`. No
transitive impact — it's already in pmox's tree via transitive pulls
in all likelihood, but explicitly imported now.

## D7. Default values — built-in, not config-file

Launch flag defaults:

| Flag         | Default |
|--------------|---------|
| `--cpu`      | 2       |
| `--mem`      | 2048    |
| `--disk`     | 20G     |
| `--wait`     | 3m      |
| `--user`     | pmox    |
| `--template` | configured default |
| `--storage`  | configured default |
| `--node`     | configured default |
| `--bridge`   | configured default |
| `--ssh-key`  | configured default |

The `configured default` values come from `internal/config.Server`
— same object slice 2 writes. Built-in literals are used only when
no configured default exists. The config file is not extended in
this slice.

**Rejected:** a `[launch]` section in `config.yaml` with user-overridable
cpu/mem/disk defaults. Premature — if two users ask for it, we add
it in a follow-up slice. For now, `--cpu 4 --mem 4096` on the CLI
is fine.

## D8. Error wrapping and exit codes

Every phase wraps errors with a phase-name prefix:

```go
return fmt.Errorf("clone template: %w", err)
return fmt.Errorf("wait for clone task: %w", err)
return fmt.Errorf("tag vm %d: %w (vm exists on cluster, run pmox delete %d)", vmid, err, vmid)
return fmt.Errorf("resize disk: %w", err)
return fmt.Errorf("push cloud-init config: %w", err)
return fmt.Errorf("start vm %d: %w", vmid, err)
return fmt.Errorf("wait for start task: %w", err)
// wait-IP returns its own message per D-T3
return fmt.Errorf("wait for ssh on %s: %w", ip, err)
```

Exit code mapping (via `internal/exitcode.From`):
- `pveclient.ErrUnauthorized` → `ExitAuth`
- `pveclient.ErrNotFound` → `ExitConfig` (usually a bad template ID)
- `pveclient.ErrAPIError` → `ExitRemote`
- `context.DeadlineExceeded` / `ErrTimeout` → `ExitTimeout`
- any other error → `ExitGeneric`

These codes already exist from slice 1.

## D9. Testing — fake PVE server + state-machine walk

One integration test file, `internal/launch/launch_test.go`, that
spins up an `httptest.Server` implementing every endpoint the state
machine calls. The test asserts:

- `NextID` is called exactly once
- `Clone` is called with the right form
- `WaitTask` is called after Clone and after Start
- `SetConfig` is called **twice** — once for `tags=pmox` alone
  (before resize), once for the full kv map (after resize)
- `Resize` is called between the two SetConfig calls
- `Start` is called after the second SetConfig
- `AgentNetwork` is polled until the mock returns interfaces with
  a usable IPv4
- SSH wait is skipped (`--no-wait-ssh` equivalent via `Options`)

The mock server is stateful (a counter) so `AgentNetwork` can return
"agent not running" on the first call and a real response on the
second — exercising the poll loop.

**Rejected:** mocking `pveclient.Client` with an interface. The real
client is already stdlib HTTP; an `httptest.Server` mock is equally
cheap and exercises the actual JSON parsing, which an interface mock
wouldn't. No new interface introduced.

A separate unit test covers `pickIPv4` against the D-T3 heuristic
with table cases.

## D10. `--no-wait-ssh` vs `--wait`

`--wait <dur>` bounds the *total* launch wall time for the IP poll +
SSH handshake phases. It does **not** bound the PVE task waits
(Clone/Start) — those have their own per-task timeouts (D3 of
pveclient-core). In practice:

- `--wait 3m` → 180s budget split between wait-IP and wait-SSH
- wait-IP gets priority; whatever's left goes to wait-SSH
- `--no-wait-ssh` skips the SSH phase entirely, so the full
  `--wait` budget applies to IP discovery

**Rejected:** separate `--wait-ip` and `--wait-ssh` flags. Too many
knobs for v1. If users ask, split them later.
