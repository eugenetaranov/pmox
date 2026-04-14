## D1. Hook is an interface, three implementations

```go
// internal/hook/hook.go
type Hook interface {
    Name() string
    Run(ctx context.Context, env Env, stdout, stderr io.Writer) error
}

type Env struct {
    IP, Name string
    VMID     int
    User     string // SSH user, from --user or default
    SSHKey   string // private key path, for Ansible
}
```

Three implementations:

```go
type PostCreateHook struct { Path string }       // --post-create
type TackHook        struct { ConfigPath string } // --tack
type AnsibleHook     struct { PlaybookPath string } // --ansible
```

The CLI layer picks one based on flag precedence, wraps it in
the interface, passes through `launch.Options.Hook`. `launch.Run`
calls `opts.Hook.Run(ctx, env, stdout, stderr)` after wait-SSH if
`opts.Hook != nil`.

**Why an interface and not a tagged union struct:** each impl has
different config fields and different argv construction. A single
struct with `Kind` plus optional fields for each kind works but
every method call has to switch on `Kind`. The interface is one
line cleaner per method.

**Rejected:** making `Hook` a function type (`func(ctx, env) error`).
Would work for two of the three, but `Name()` is needed for the
log line (`running --tack hook: ...`) and a function type can't
carry that.

## D2. Direct exec, not shell

```go
// PostCreateHook.Run
cmd := exec.CommandContext(ctx, h.Path)
cmd.Env = append(os.Environ(),
    "PMOX_IP="+env.IP,
    "PMOX_VMID="+strconv.Itoa(env.VMID),
    "PMOX_NAME="+env.Name,
    "PMOX_USER="+env.User,
)
cmd.Stdout = stdout
cmd.Stderr = stderr
return cmd.Run()
```

**Why direct exec, not `sh -c`:**
- Users pass a path (`./provision.sh`, `/usr/local/bin/configure`),
  not a shell string. A shell wrapper adds nothing and adds
  injection risk if the path contains spaces or quotes.
- The user's script starts with its own shebang line. If it's a
  Python script with `#!/usr/bin/env python3`, we shouldn't care.
- Matches tack's post-apply-hook behavior.

**Env vars, not args:** simpler contract. No argv parsing, no
quoting. The script reads `$PMOX_IP`. Anyone can shell-script
against this without docs.

**Working directory:** inherited. We don't chdir. If the user
wants a specific cwd, their shebang or their script handles it.

## D3. Tack and Ansible — no reinventing argv

```go
// TackHook.Run
cmd := exec.CommandContext(ctx, "tack", "apply",
    "--host", env.IP,
    "--user", env.User,
    h.ConfigPath,
)
```

```go
// AnsibleHook.Run
cmd := exec.CommandContext(ctx, "ansible-playbook",
    "-i", env.IP+",",          // trailing comma makes it an inline inventory
    "-u", env.User,
    "--private-key", env.SSHKey,
    "-e", fmt.Sprintf("pmox_vmid=%d", env.VMID),
    "-e", "pmox_name="+env.Name,
    h.PlaybookPath,
)
```

Both inherit `cmd.Env = os.Environ()` so users can pass through
their own `ANSIBLE_*` or `TACK_*` env vars. Both stream
stdout/stderr live.

**Missing binary:** if `exec.LookPath("tack")` fails, return
`fmt.Errorf("tack hook: tack binary not found on PATH. install tack from https://github.com/tackhq/tack or pass --post-create instead")`.
Same shape for `ansible-playbook`.

**Rejected:** a `--tack-args` / `--ansible-args` passthrough.
Would let users override our argv, but also explodes the
surface and introduces quoting. For v1, the above argv is
fixed; if someone needs a custom invocation, they use
`--post-create` with a wrapper script.

**Ansible `-i host,`**: the trailing comma is Ansible's syntax for
an inline inventory with one host. Not a typo.

## D4. Mutual exclusion at parse time

```go
// cmd/pmox/launch.go
func resolveHook(cmd *cobra.Command, opts *launch.Options) error {
    postCreate, _ := cmd.Flags().GetString("post-create")
    tack, _ := cmd.Flags().GetString("tack")
    ansible, _ := cmd.Flags().GetString("ansible")
    set := 0
    if postCreate != "" { set++ }
    if tack != "" { set++ }
    if ansible != "" { set++ }
    if set > 1 {
        return errors.New("--post-create, --tack, and --ansible are mutually exclusive; pick one")
    }
    switch {
    case postCreate != "": opts.Hook = &hook.PostCreateHook{Path: postCreate}
    case tack != "":       opts.Hook = &hook.TackHook{ConfigPath: tack}
    case ansible != "":    opts.Hook = &hook.AnsibleHook{PlaybookPath: ansible}
    }
    return nil
}
```

**Rejected:** Cobra's `cmd.MarkFlagsMutuallyExclusive` — works for
two flags but the error message is terse ("only one of X, Y"),
and we want a message that names the purpose ("pick one"). Also,
the check runs in PreRun in Cobra, we want it in the hook
resolution path for easier testing.

## D5. Strict vs lenient failure

```go
// internal/launch/launch.go phase 10
if opts.Hook != nil {
    err := opts.Hook.Run(ctx, hookEnv, os.Stdout, os.Stderr)
    if err != nil {
        fmt.Fprintf(os.Stderr, "warning: %s hook failed: %v\n", opts.Hook.Name(), err)
        if opts.StrictHooks {
            return &HookError{Hook: opts.Hook.Name(), Err: err}
        }
    }
}
```

`HookError` is a sentinel type mapped to `ExitHook` in
`exitcode.From`:

```go
// internal/exitcode/exitcode.go
const ExitHook = 7  // next after the existing codes

// From
var he *launch.HookError
if errors.As(err, &he) { return ExitHook }
```

**Why a type, not `ErrHook`:** we want the mapping to key off a
structural type so `fmt.Errorf("wrap: %w", hookErr)` still
triggers the mapping via `errors.As`. A sentinel error would
work too but would lose the hook name in the wrap chain.

**Lenient mode semantics:**
- Hook runs
- Hook fails
- Launch prints a warning to stderr with the error
- Launch prints a success message to stdout with the IP
- Launch exits 0

So the user sees:

```
$ pmox launch web1 --tack ./tack.yaml
launched web1 (vmid=104, ip=192.168.1.43)
warning: tack hook failed: exit status 1
$ echo $?
0
```

**Strict mode:** same sequence, but the warning is followed by
`exit ExitHook`. The VM still exists; cleanup is manual via
`pmox delete`.

**Rejected:** auto-deleting the VM on strict hook failure. Would
violate D-T1's "no auto-rollback" principle. If the user wants
cleanup, they `pmox delete`.

## D6. `--no-wait-ssh` + hook interaction

```go
if opts.NoWaitSSH && opts.Hook != nil {
    fmt.Fprintln(opts.Stderr, "warning: --no-wait-ssh set; hook will not run")
}
```

Warning, not error. The user may have a reason (hook is supposed
to run against the IP through some non-SSH channel, e.g. a
bootstrap service). We just skip the hook phase.

**Rejected:** erroring. Combining `--no-wait-ssh` and `--post-create`
is weird but not nonsensical — a user could have a hook that
waits for cloud-init to settle on its own.

## D7. Hook timeout budget

The overall `--wait` budget splits roughly:
- wait-IP: up to `wait - 10s`
- wait-SSH: the remaining time
- hook: whatever is left, with a **30s minimum** floor

If wait-IP + wait-SSH consumed 165 of a 180s budget, the hook
still gets 30s. If they consumed 5s each, the hook gets 170s.

**Why a floor:** without it, `--wait 3m` on a slow cluster would
give the hook 0s and fail immediately. 30s lets even a pathological
case at least try.

**Rejected:** a separate `--hook-timeout` flag. Fourth timeout
flag on `launch`. Too much surface. If users need it, add in v2.

```go
hookBudget := remaining(deadline)
if hookBudget < 30*time.Second {
    hookBudget = 30 * time.Second
}
hookCtx, cancel := context.WithTimeout(ctx, hookBudget)
defer cancel()
```

## D8. Testing — fake hook binaries

`internal/hook/hook_test.go` uses small inline scripts written to
a temp dir:

```go
func writeTempScript(t *testing.T, body string) string {
    t.Helper()
    path := filepath.Join(t.TempDir(), "hook.sh")
    os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755)
    return path
}
```

Test cases:
- `TestPostCreateHook_HappyPath`: script that `echo $PMOX_IP && echo $PMOX_VMID` — assert stdout contains the IP and VMID
- `TestPostCreateHook_NonZeroExit`: script that `exit 1` — assert `Run` returns an error
- `TestPostCreateHook_ContextCancel`: script that `sleep 10` — cancel after 100ms, assert `Run` returns `ctx.Err()` (the exec package propagates it)
- `TestTackHook_MissingBinary`: override `$PATH` to empty, assert error contains `tack binary not found`
- `TestAnsibleHook_ArgvShape`: use a stub script named `ansible-playbook` in a test PATH dir that prints its argv and exits 0; assert the captured argv matches the design D3 shape

**`launch.Run` hook phase tests:**
- `TestRun_HookSuccess`: passes a trivial post-create script through `Options.Hook`, asserts the hook ran after wait-SSH (via call order captured by the fake PVE helper + test script output)
- `TestRun_HookFailureLenient`: hook script exits 1, `StrictHooks=false`, assert `Run` returns nil
- `TestRun_HookFailureStrict`: same but `StrictHooks=true`, assert `Run` returns a `*HookError`
- `TestRun_HookSkippedOnNoWaitSSH`: `NoWaitSSH=true` and a hook set, assert the hook script is never executed (use a flag file it would create)

## D9. What hooks can see in env

Final env var contract (locked by spec.md):

| Var         | Value                              |
|-------------|------------------------------------|
| `PMOX_IP`   | the discovered IPv4                |
| `PMOX_VMID` | decimal VMID                       |
| `PMOX_NAME` | the launch name                    |
| `PMOX_USER` | the SSH user (`--user` or default) |
| `PMOX_NODE` | the PVE node hosting the VM        |
| (inherited) | everything from `os.Environ()`     |

The user's existing shell env (`$HOME`, `$PATH`, `$AWS_PROFILE`, ...)
passes through. Only the `PMOX_*` prefix is added.

**`PMOX_NODE`** included because users writing automation around
pmox often want to know which PVE node the VM landed on (for
affinity/anti-affinity decisions in scripts).

**Not included:** `PMOX_SSH_KEY_PATH`. The script is running in
the user's environment and already has access to `~/.ssh`; we
don't need to re-expose it.
