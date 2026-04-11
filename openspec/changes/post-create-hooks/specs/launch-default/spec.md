## ADDED Requirements

### Requirement: Hook phase

The `launch.Run` state machine SHALL gain a final phase after wait-SSH that invokes `opts.Hook.Run` when a hook is configured.

#### Scenario: Hook runs after wait-SSH
- **WHEN** `opts.Hook` is non-nil and wait-SSH has succeeded
- **THEN** the launcher SHALL call `opts.Hook.Run(ctx, env, stdout, stderr)`
- **AND** the call SHALL happen after the SSH handshake succeeded
- **AND** `env` SHALL carry the discovered IP, VMID, name, user, and node

#### Scenario: No hook means no phase 10
- **WHEN** `opts.Hook` is nil
- **THEN** the launcher SHALL return immediately after wait-SSH
- **AND** the success message SHALL still be printed

### Requirement: HookError type

The `internal/launch` package SHALL expose `HookError` as a named error type carrying the hook name and the underlying error, so `internal/exitcode` can map it to `ExitHook` via `errors.As`.

#### Scenario: HookError wraps the underlying error
- **WHEN** a hook fails in strict mode
- **THEN** the returned error SHALL be a non-nil `*launch.HookError`
- **AND** `errors.Is` against the wrapped error SHALL still work
- **AND** the error message SHALL contain the hook name

### Requirement: Options carry hook and strict flag

The `launch.Options` struct SHALL gain fields `Hook hook.Hook` and `StrictHooks bool`.

#### Scenario: Options expose the hook fields
- **WHEN** a caller constructs `launch.Options{Hook: h, StrictHooks: true}`
- **AND** calls `launch.Run`
- **THEN** the hook SHALL be invoked per the hook-phase rules
- **AND** strict-mode failure handling SHALL apply
