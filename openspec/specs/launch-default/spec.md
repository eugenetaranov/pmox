## Purpose

Native `pmox launch <name>` command that provisions a VM from a cloud-init-enabled template on the resolved Proxmox cluster, pushes a built-in cloud-init config, waits for the guest agent to report an IP, and optionally waits for sshd to be reachable before returning success.

## Requirements

### Requirement: `pmox launch` command

The CLI SHALL expose `pmox launch <name>` which provisions a new
VM on the resolved Proxmox cluster from a cloud-init-enabled
template and waits for the VM to become reachable.

#### Scenario: Happy path launches a running VM
- **WHEN** `pmox launch web1` is invoked against a fake PVE server returning canned success responses
- **THEN** the command SHALL exit 0
- **AND** stdout SHALL include the allocated VMID and the discovered IPv4 address
- **AND** the 9-step state machine SHALL have issued calls in order: `NextID`, `Clone`, `WaitTask`, `SetConfig(tags)`, `Resize`, `SetConfig(kv)`, `Start`, `WaitTask`, `AgentNetwork` (polled)

#### Scenario: Missing template ID surfaces a configuration error
- **WHEN** `pmox launch web1` is invoked and no template is configured and `--template` is not passed
- **THEN** the command SHALL exit with `ExitConfig`
- **AND** the error message SHALL name the missing setting and suggest `pmox configure`

### Requirement: Tag before resize

After a successful clone, the launcher SHALL set `tags=pmox` on the
new VM **before** issuing the resize call, so that partial failures
after clone leave a tagged VM on the cluster that can be cleaned up
by `pmox delete` without a `--force` flag.

#### Scenario: Tag call precedes resize
- **WHEN** the launcher processes a successful clone
- **THEN** the next call against the cloned VMID SHALL be `SetConfig` with `{"tags": "pmox"}`
- **AND** the following call SHALL be `Resize`

#### Scenario: Tag failure leaves cleanable VM
- **WHEN** the `SetConfig(tags)` call fails
- **THEN** the launcher SHALL return an error containing the VMID
- **AND** the error message SHALL direct the user to run `pmox delete <vmid>`
- **AND** the launcher SHALL NOT issue any rollback API calls

### Requirement: No automatic rollback

The launcher SHALL NOT attempt to auto-delete or roll back a VM on
any failure after step 2 (clone). The VM remains on the cluster and
the user is responsible for cleanup via `pmox delete`.

#### Scenario: Start failure leaves VM on the cluster
- **WHEN** the `Start` call fails after config push succeeds
- **THEN** the launcher SHALL return an error naming the VMID
- **AND** the launcher SHALL NOT call `Delete`

### Requirement: Cloud-init config always routes through a snippet

`launch.Run` SHALL always read a per-server cloud-init file,
validate it, upload it via `PostSnippet`, and set `cicustom` on
the target VM. The launcher SHALL NOT set the built-in
`ciuser`, `cipassword`, or `sshkeys` keys. `BuildBuiltinKV` is
removed; `BuildCustomKV` is the only config-builder.

The resolved cloud-init path is populated into
`launch.Options.CloudInitPath` by the caller (the CLI layer)
from `config.CloudInitPath(canonicalURL)` before `Run` is
invoked. `launch.Options` SHALL NOT carry a `NoSSHKeyCheck`
field.

#### Scenario: Happy-path launch uploads a snippet and sets cicustom
- **WHEN** a caller constructs `launch.Options{CloudInitPath: "/tmp/cloud-init.yaml", Storage: "local", ...}`
- **AND** the file at that path exists, is valid UTF-8, ≤64 KiB, and the resolved storage supports snippets
- **AND** calls `launch.Run(ctx, opts)`
- **THEN** the launcher SHALL call `PostSnippet` with the file contents and filename `pmox-<vmid>-user-data.yaml`
- **AND** SHALL call `SetConfig` with a kv map whose `cicustom` equals `user=local:snippets/pmox-<vmid>-user-data.yaml`
- **AND** the kv map SHALL NOT contain `ciuser`, `cipassword`, or `sshkeys`

#### Scenario: BuildCustomKV is the only config-builder
- **WHEN** the launcher is inspected at the config phase
- **THEN** there SHALL be no code path that produces a config map via `BuildBuiltinKV`

### Requirement: File read and validation happens before clone

The launcher SHALL read and validate the cloud-init file
**before** calling `NextID` or `Clone`, so that a bad or
missing file fails fast without creating an orphan VM on the
cluster.

#### Scenario: Missing file aborts before clone
- **WHEN** `opts.CloudInitPath` points at a non-existent path
- **THEN** the launcher SHALL return an error wrapping the underlying not-exist error
- **AND** the error SHALL include the substring `pmox configure --regen-cloud-init`
- **AND** SHALL NOT issue any PVE API call

#### Scenario: Invalid file aborts before clone
- **WHEN** `opts.CloudInitPath` points at a binary or oversized file
- **THEN** the launcher SHALL return a validation error before calling `NextID`
- **AND** SHALL NOT issue any PVE API call

### Requirement: IP discovery via qemu-guest-agent

The launcher SHALL poll `AgentNetwork` to discover the VM's IP
address. It SHALL NOT fall back to DHCP lease inspection or any
other mechanism.

#### Scenario: Agent reports a usable IPv4
- **WHEN** `AgentNetwork` returns an `eth0` interface with an IPv4 outside `127.0.0.0/8` and `169.254.0.0/16`
- **THEN** the launcher SHALL return that IPv4 as the launch result

#### Scenario: Agent is not yet running
- **WHEN** `AgentNetwork` returns an error or an empty result
- **THEN** the launcher SHALL wait the poll interval and retry
- **AND** SHALL NOT distinguish between "agent not running" and "no interfaces yet"

#### Scenario: Timeout surfaces actionable error
- **WHEN** the agent never reports a usable IP within the configured wait budget
- **THEN** the launcher SHALL return an error containing the VMID and the text `install qemu-guest-agent in your template and re-run launch`

### Requirement: IP picker heuristic

When `AgentNetwork` reports multiple interfaces, the launcher SHALL
pick one IPv4 address using a deterministic heuristic.

#### Scenario: Virtual interfaces are skipped
- **WHEN** interfaces include `lo`, `docker0`, `br-abc123`, `veth*`, `cni0`, `virbr0`, `tun0`
- **THEN** the picker SHALL exclude those before selecting

#### Scenario: Loopback and link-local addresses are excluded
- **WHEN** the candidate set includes IPv4 addresses in `127.0.0.0/8` or `169.254.0.0/16`
- **THEN** the picker SHALL skip those addresses

#### Scenario: Fallback to first non-loopback non-link-local
- **WHEN** no interface survives the prefix filter
- **THEN** the picker SHALL fall back to the first non-loopback non-link-local IPv4 across all interfaces

#### Scenario: No usable IPv4 returns empty
- **WHEN** no IPv4 passes the heuristic
- **THEN** the picker SHALL return the empty string so the caller can keep polling

### Requirement: SSH reachability wait

After an IP is discovered, the launcher SHALL attempt an SSH
handshake against `<ip>:22` to confirm sshd is actually ready.

#### Scenario: Handshake success counts as ready
- **WHEN** a TCP connection to `:22` succeeds and sshd exchanges a protocol banner
- **THEN** the launcher SHALL consider the VM reachable
- **AND** SHALL proceed to success even if authentication would fail

#### Scenario: `--no-wait-ssh` skips the phase
- **WHEN** the caller passes `--no-wait-ssh`
- **THEN** the launcher SHALL return success as soon as the IP is known

### Requirement: Verbose server-resolution log line

When `-v` is active, the launcher SHALL emit one stderr line
identifying which server was selected and why, per the D-T4
format.

#### Scenario: Verbose launch logs the selected server
- **WHEN** `pmox -v launch web1` is invoked
- **THEN** stderr SHALL contain one line matching `using server <url> (<reason>)`
- **AND** the line SHALL appear before the first PVE API call

### Requirement: Launch flags

The `launch` command SHALL accept the following flags:

- `--cpu N`, `--mem MB`, `--disk NG`
- `--template <id|name>`, `--storage <id>`, `--snippet-storage <id>`, `--node <name>`, `--bridge <name>`
- `--user <name>`, `--ssh-key <path>`
- `--wait <duration>` (default 3m), `--no-wait-ssh`

`--snippet-storage` overrides `server.snippet_storage` on a single
invocation and SHALL NOT affect disk-storage resolution. When neither
the flag nor `server.snippet_storage` is set, the launcher SHALL fall
back to the resolved disk storage and emit a stderr warning naming the
fallback pool and suggesting `pmox configure` as the permanent fix.

#### Scenario: Unset flags fall back to configured defaults
- **WHEN** a flag is not passed and the resolved server has a corresponding default in `config.yaml`
- **THEN** the launcher SHALL use the configured default

#### Scenario: Unset flags with no configured default use built-in defaults
- **WHEN** a flag is not passed and no configured default exists
- **THEN** the launcher SHALL use the built-in default from the flag table in the design doc

### Requirement: Exit code mapping

The launcher SHALL map error classes to the typed exit codes from
`internal/exitcode`:

- `ErrUnauthorized` → `ExitAuth`
- `ErrNotFound` → `ExitConfig`
- `ErrAPIError` → `ExitRemote`
- `ErrTimeout` / `context.DeadlineExceeded` → `ExitTimeout`
- any other error → `ExitGeneric`

#### Scenario: 401 from clone surfaces ExitAuth
- **WHEN** `Clone` returns an error wrapping `ErrUnauthorized`
- **THEN** the process SHALL exit with `ExitAuth`

#### Scenario: Bad template ID surfaces ExitConfig
- **WHEN** `Clone` returns an error wrapping `ErrNotFound`
- **THEN** the process SHALL exit with `ExitConfig`

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
