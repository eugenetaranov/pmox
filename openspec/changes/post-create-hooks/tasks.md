## 1. internal/hook — Hook interface and Env

- [ ] 1.1 Create `internal/hook/hook.go`
- [ ] 1.2 Declare `type Env struct { IP, Name, User, Node string; VMID int; SSHKey string }`
- [ ] 1.3 Declare `type Hook interface { Name() string; Run(ctx context.Context, env Env, stdout, stderr io.Writer) error }`
- [ ] 1.4 Helper `func setenv(env Env) []string` returns the `PMOX_*` env var slice appended to `os.Environ()`

## 2. PostCreateHook

- [ ] 2.1 `type PostCreateHook struct { Path string }`
- [ ] 2.2 `Name() string` returns `"post-create"`
- [ ] 2.3 `Run` uses `exec.CommandContext(ctx, h.Path)` — no shell wrapping
- [ ] 2.4 Sets `cmd.Env = setenv(env)`, `cmd.Stdout = stdout`, `cmd.Stderr = stderr`
- [ ] 2.5 Returns `cmd.Run()` directly; any non-zero exit surfaces as `*exec.ExitError`

## 3. TackHook

- [ ] 3.1 `type TackHook struct { ConfigPath string }`
- [ ] 3.2 `Name() string` returns `"tack"`
- [ ] 3.3 `Run` first calls `exec.LookPath("tack")`; on error returns `errors.New("tack binary not found on PATH. install tack from https://github.com/tackhq/tack or pass --post-create instead")`
- [ ] 3.4 Argv: `tack apply --host <ip> --user <user> <config-path>`
- [ ] 3.5 Inherit environ; stream stdout/stderr

## 4. AnsibleHook

- [ ] 4.1 `type AnsibleHook struct { PlaybookPath string }`
- [ ] 4.2 `Name() string` returns `"ansible"`
- [ ] 4.3 `Run` calls `exec.LookPath("ansible-playbook")`; on error returns not-found message
- [ ] 4.4 Argv: `ansible-playbook -i <ip>, -u <user> --private-key <key> -e pmox_vmid=<vmid> -e pmox_name=<name> <playbook>`
- [ ] 4.5 The trailing comma after the IP is required by Ansible's inline inventory syntax — document with a comment
- [ ] 4.6 Inherit environ; stream stdout/stderr

## 5. internal/hook tests

- [ ] 5.1 Create `internal/hook/hook_test.go`
- [ ] 5.2 `writeTempScript(t, body string) string` helper — writes `#!/bin/sh\n<body>` to a 0755 temp file
- [ ] 5.3 `TestPostCreateHook_EnvVars`: script echoes `$PMOX_IP $PMOX_VMID $PMOX_NAME`; capture stdout and assert the values
- [ ] 5.4 `TestPostCreateHook_NonZeroExit`: script `exit 1`; assert error is `*exec.ExitError`
- [ ] 5.5 `TestPostCreateHook_ContextCancel`: script `sleep 30`; cancel ctx after 100ms; assert `Run` returns within 1s and error wraps `ctx.Err()`
- [ ] 5.6 `TestTackHook_LookPathFail`: set `PATH=""` via `t.Setenv`; assert error contains `tack binary not found`
- [ ] 5.7 `TestTackHook_ArgvShape`: prepend a temp dir to PATH containing a stub `tack` script that echoes its argv; assert captured argv is `apply --host 192.168.1.10 --user ubuntu ./tack.yaml`
- [ ] 5.8 `TestAnsibleHook_ArgvShape`: same technique with a stub `ansible-playbook` script; assert argv includes `-i 192.168.1.10,`, `-u ubuntu`, `--private-key`, `-e pmox_vmid=104`, `-e pmox_name=web1`, and the playbook path last
- [ ] 5.9 `TestAnsibleHook_LookPathFail`: empty PATH; assert error contains `ansible-playbook binary not found`

## 6. exitcode — ExitHook

- [ ] 6.1 Add `const ExitHook = 7` to `internal/exitcode/exitcode.go` (use the next available code — check current max; if 7 is taken, bump to next)
- [ ] 6.2 Update `func From(err error)` to check `errors.As(err, &hookErr)` against `*launch.HookError` (import cycle note: exitcode already imports launch? If not, add the check via an interface `type hookErr interface { IsHookError() }` implemented by `*launch.HookError` to avoid the cycle)
- [ ] 6.3 Test `TestFromHookError` in `internal/exitcode/exitcode_test.go` — wraps a `*launch.HookError` in `fmt.Errorf("...: %w", ...)`, asserts `From` returns `ExitHook`

## 7. internal/launch — HookError type

- [ ] 7.1 Add `type HookError struct { Hook string; Err error }` in `internal/launch/launch.go`
- [ ] 7.2 `func (e *HookError) Error() string` returns `fmt.Sprintf("%s hook failed: %v", e.Hook, e.Err)`
- [ ] 7.3 `func (e *HookError) Unwrap() error` returns `e.Err`
- [ ] 7.4 `func (e *HookError) IsHookError() {}` — marker method for exitcode package to avoid import cycle

## 8. internal/launch — Options and phase 10

- [ ] 8.1 Extend `launch.Options` with `Hook hook.Hook`, `StrictHooks bool`
- [ ] 8.2 After wait-SSH returns success, in `Run`, if `opts.Hook != nil` and not `opts.NoWaitSSH`, run the hook phase
- [ ] 8.3 Compute hook budget: `hookBudget = time.Until(overallDeadline)`; if `hookBudget < 30*time.Second`, set to `30*time.Second`
- [ ] 8.4 Create `hookCtx` via `context.WithTimeout(ctx, hookBudget)`; defer cancel
- [ ] 8.5 Build `env := hook.Env{IP: ip, Name: opts.Name, VMID: vmid, User: opts.User, Node: opts.Node, SSHKey: opts.SSHKeyPath}`
- [ ] 8.6 Call `opts.Hook.Run(hookCtx, env, os.Stdout, os.Stderr)`; on error, write warning line to `opts.Stderr` via `fmt.Fprintf("warning: %s hook failed: %v\n", opts.Hook.Name(), err)`
- [ ] 8.7 If `opts.StrictHooks`, return `&HookError{Hook: opts.Hook.Name(), Err: err}`; otherwise return nil (phase 10 swallows)
- [ ] 8.8 Add a second branch at the top of phase 10: if `opts.NoWaitSSH && opts.Hook != nil`, print `warning: --no-wait-ssh set; hook will not run` to `opts.Stderr` and skip

## 9. cmd/pmox — flag wiring

- [ ] 9.1 In `cmd/pmox/launch.go`, add flags: `--post-create`, `--tack`, `--ansible`, `--strict-hooks`
- [ ] 9.2 Mutual exclusion resolver `resolveHook(cmd *cobra.Command) (hook.Hook, error)`; error message: `"--post-create, --tack, and --ansible are mutually exclusive; pick one of <flag1> or <flag2>"` naming the two (or three) that were set
- [ ] 9.3 Wire `opts.Hook` and `opts.StrictHooks` from the flag values
- [ ] 9.4 On exclusion error, return immediately with `ExitConfig` — no server resolution, no API calls
- [ ] 9.5 Same flag set added to `cmd/pmox/clone.go`
- [ ] 9.6 `--help` output for both commands documents all four flags

## 10. launch_test.go — hook phase tests

- [ ] 10.1 `TestRun_HookSuccess`: write a temp script that creates a marker file; pass as `PostCreateHook`; assert marker file exists after `Run` returns
- [ ] 10.2 `TestRun_HookFailureLenient`: script exits 1, `StrictHooks=false`; assert `Run` returns nil and stderr has the warning line
- [ ] 10.3 `TestRun_HookFailureStrict`: same script, `StrictHooks=true`; assert `Run` returns error of type `*HookError`
- [ ] 10.4 `TestRun_HookSkippedOnNoWaitSSH`: script writes a marker; `NoWaitSSH=true` + hook set; assert marker does NOT exist after `Run` and stderr has the skip warning
- [ ] 10.5 `TestRun_HookTimeoutFloor`: construct `opts.Wait` such that wait-IP + wait-SSH consume nearly all budget; run a hook that `sleep 5`; assert hook is given at least 30s (use the fake helper to observe the ctx deadline)

## 11. cmd/pmox tests — mutual exclusion

- [ ] 11.1 `cmd/pmox/launch_test.go`: case where `--post-create` and `--tack` are both set; assert `Execute` returns error containing `mutually exclusive`
- [ ] 11.2 Assert zero PVE API calls were made (use the fake helper's request log)
- [ ] 11.3 Same for `--tack` + `--ansible`
- [ ] 11.4 Single-flag cases succeed

## 12. Verification

- [ ] 12.1 `go build ./...` passes
- [ ] 12.2 `go test ./... -race` passes
- [ ] 12.3 `make lint` passes
- [ ] 12.4 `pmox launch --help` shows the four new flags
- [ ] 12.5 Manual smoke: launch with a trivial `--post-create ./echo.sh`; observe the env vars arrive
- [ ] 12.6 Manual smoke: launch with `--tack` against a minimal tack config; observe tack runs and succeeds
- [ ] 12.7 Manual smoke: launch with `--post-create ./fail.sh` (script that `exit 1`); observe warning and exit 0; re-run with `--strict-hooks`, observe exit code `ExitHook`
