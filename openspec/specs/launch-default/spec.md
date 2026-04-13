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

### Requirement: Built-in cloud-init config

The launcher SHALL push a default cloud-init configuration via PVE
config keys (not via `cicustom` / snippets) when `--cloud-init` is
not supplied. The config SHALL include the configured SSH public
key, a default user account, `agent=1`, and DHCP on the primary
interface.

#### Scenario: Config map contains the required cloud-init keys
- **WHEN** the launcher reaches the config phase and `--cloud-init` was not passed
- **THEN** the `SetConfig` kv map SHALL contain keys `ciuser`, `sshkeys`, `ipconfig0`, `agent`, `memory`, `cores`, `name`
- **AND** `ipconfig0` SHALL equal `ip=dhcp`
- **AND** `agent` SHALL equal `1`

#### Scenario: Built-in config does not touch snippets storage
- **WHEN** the launcher runs in built-in cloud-init mode
- **THEN** the launcher SHALL NOT issue any request against the PVE storage snippets endpoint
- **AND** the config kv map SHALL NOT contain a `cicustom` key

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
