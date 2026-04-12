## Context

After `pmox launch` or `pmox clone`, the operator sees the VM's IP printed
to stdout and must manually assemble an `ssh` command with the right user,
key path, and IP. The codebase already has every building block needed for
an automated connect flow:

- `vm.Resolve` → name/VMID to node + VMID + tags
- `pveclient.GetStatus` → running/stopped
- `pveclient.Start` + `pveclient.WaitTask` → power on
- `launch.WaitForIP` → poll guest agent for IPv4
- `launch.WaitForSSH` → handshake readiness probe
- `config.Server.SSHPubkey` → configured public key path

The missing piece is a command that strings these together and exec's
the system `ssh` binary.

## Goals / Non-Goals

**Goals:**
- `pmox shell <name|vmid>` opens an interactive SSH session.
- `pmox exec <name|vmid> -- <command> [args...]` runs a command over SSH
  and exits with the remote exit code.
- Both auto-start stopped VMs (Start → WaitForIP → WaitForSSH).
- Both enforce the pmox tag check; `--force` bypasses it.
- Both use the system `ssh` binary, not a Go SSH library.
- Default user is `pmox`; default identity key is derived from the
  configured public key path by stripping `.pub`.
- Guest agent failure is a hard error with guidance.

**Non-Goals:**
- SCP / file transfer (follow-up).
- SSH port forwarding / ProxyJump flags (follow-up).
- Persistent connection multiplexing.
- Storing per-VM metadata (user, key) locally.
- A "primary VM" default when no name is given.
- Windows support.

## Decisions

### D1 — Exec the system `ssh` binary, don't use `x/crypto/ssh` for sessions

Use `syscall.Exec` (on Unix) to replace the pmox process with `ssh`.
This gives us PTY handling, agent forwarding, `~/.ssh/config` inheritance,
escape sequences, and SIGWINCH — all for free. The Go SSH library would
require reimplementing all of that.

For `exec` (non-interactive), use `os/exec.Command` instead of
`syscall.Exec` so we can capture the exit code and return it.

**Alternative considered:** `golang.org/x/crypto/ssh` session with
`session.RequestPty` + piping stdin/stdout/stderr. Rejected — PTY
handling in Go is painful, no agent forwarding, no `~/.ssh/config`
inheritance, and every tool in this niche (multipass, vagrant, docker)
execs `ssh`.

### D2 — Shared `sshConnect` helper used by both shell and exec

Both commands share identical logic up to the `ssh` invocation:

```
resolve → tag check → status check → maybe auto-start → get IP → build ssh args
```

Extract this into a shared helper in `cmd/pmox/ssh.go`:

```go
type sshTarget struct {
    IP   string
    User string
    Key  string  // private key path
}

func resolveSSHTarget(ctx context.Context, cmd *cobra.Command,
    client *pveclient.Client, arg string, f *sshFlags) (*sshTarget, error)
```

`shell` calls `resolveSSHTarget` then `syscall.Exec("ssh", ...)`.
`exec` calls `resolveSSHTarget` then `os/exec.Command("ssh", ...)`.

### D3 — Auto-start stopped VMs

When `GetStatus` returns `"stopped"`:

1. `client.Start(ctx, node, vmid)` → UPID
2. `client.WaitTask(ctx, node, upid, 120s)` — wait for PVE to power on
3. `launch.WaitForIP(ctx, client, node, vmid, 60s)` — poll guest agent
4. `launch.WaitForSSH(ctx, ip, 30s)` — verify sshd is ready

Print progress to stderr: `Starting VM "web1"...`, `Waiting for IP...`,
`Waiting for SSH...` — one line each, matching the launch command's style.

When `GetStatus` returns `"running"`:

1. `pveclient.AgentNetwork(ctx, node, vmid)` → interfaces
2. `launch.PickIPv4(interfaces)` → IP
3. If no IP found, fail: `"VM is running but guest agent returned no IP;
   is qemu-guest-agent installed?"`

### D4 — Private key derivation

The config stores `SSHPubkey` (e.g. `~/.ssh/id_ed25519.pub`). The
private key is the same path with `.pub` stripped. If the derived path
doesn't exist, error with guidance to use `--identity`.

The `--identity` / `-i` flag overrides this derivation entirely.

If neither the config nor the flag provides a key, fall back to invoking
`ssh` without `-i` — let OpenSSH's default key discovery handle it.

### D5 — SSH invocation flags

For both shell and exec:

```
ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    [-i <identity>] \
    <user>@<ip> \
    [-- command args...]   # exec only
```

`StrictHostKeyChecking=no` because guest VMs are ephemeral — host keys
change every launch. `UserKnownHostsFile=/dev/null` avoids polluting
`~/.ssh/known_hosts` with stale entries.

### D6 — `--force` bypasses tag check (consistent with delete)

Both commands check `vm.HasPMOXTag(ref.Tags)` unless `--force` is passed.
This matches `delete`'s pattern. Shell/exec are non-destructive, but the
tag check prevents accidentally connecting to hand-managed VMs that happen
to be on the same cluster.

### D7 — Exit code passthrough for exec

`pmox exec` passes through the remote command's exit code. If SSH itself
fails (connection refused, auth failure), exit with the SSH process's
exit code. This makes `pmox exec web1 -- test -f /etc/foo && echo yes`
behave as expected in scripts.

### D8 — Command registration

Both `shell` and `exec` are large enough to warrant their own file but
share enough logic that a single `cmd/pmox/ssh.go` for the shared helper
plus `newShellCmd()` and `newExecCmd()` is cleaner than three files.
Register both in `main.go`'s `init()`.

## Risks / Trade-offs

- **[Risk]** `ssh` not on PATH (unlikely on darwin/linux, pmox's only
  targets). → **Mitigation:** `exec.LookPath("ssh")` early, clear error
  message if missing.
- **[Risk]** Derived private key path doesn't match actual key (e.g.
  user configured a `.pub` that has no private counterpart). →
  **Mitigation:** check file exists before invoking ssh; error message
  suggests `--identity`.
- **[Risk]** Guest agent not running or no IP assigned. → **Mitigation:**
  hard fail with clear message. No fallback — the guest agent is a
  requirement for pmox VMs (template bakes it in).
- **[Trade-off]** `StrictHostKeyChecking=no` is a security downgrade.
  Acceptable because guest VMs are ephemeral and launched by the same
  operator. Document in README.
- **[Trade-off]** `syscall.Exec` replaces the process, so no cleanup
  code runs after shell. Acceptable — there's nothing to clean up.
