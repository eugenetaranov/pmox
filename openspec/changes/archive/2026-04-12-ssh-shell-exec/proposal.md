## Why

After `pmox launch` prints the VM's IP, the operator has to manually type
`ssh -i ~/.ssh/id_ed25519 pmox@<ip>` every time they want to connect. This
is the single most common next step after launch, and it requires remembering
the user, key path, and IP. Multipass solves this with `multipass shell`;
pmox should match that baseline so the launch→connect workflow is one command.

## What Changes

- `pmox shell <name|vmid>` SHALL open an interactive SSH session to a
  pmox-tagged VM. It resolves the VM, discovers its IP via the QEMU guest
  agent, and `exec`s the system `ssh` binary. If the VM is stopped, shell
  auto-starts it and waits for SSH readiness before connecting.
- `pmox exec <name|vmid> -- <command> [args...]` SHALL run a single command
  on a pmox-tagged VM over SSH and return its output/exit code. Same
  resolution and auto-start behavior as shell, but non-interactive.
- Both commands accept `--user` / `-u` (default `"pmox"`) and
  `--identity` / `-i` (default: derived from the configured SSH public key
  by stripping the `.pub` suffix).
- Both commands enforce the pmox tag check (consistent with `delete`). Use
  `--force` to connect to untagged VMs.
- SSH is invoked with `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null`
  since guest VMs are ephemeral and their host keys change on every launch.

## Capabilities

### New Capabilities
- `ssh-shell-exec`: Interactive shell and single-command execution over SSH
  to pmox-managed guest VMs, including VM resolution, auto-start, IP
  discovery, and system `ssh` exec.

### Modified Capabilities
None — `pveclient.Start` already exists.

## Impact

- **Code**: New `cmd/pmox/shell.go`, `cmd/pmox/exec.go` (or combined),
  registration in `cmd/pmox/main.go`. Reuses `vm.Resolve`, `pveclient.AgentNetwork`,
  `launch.WaitForIP`, `launch.WaitForSSH`.
- **Dependencies**: No new dependencies. Uses `os/exec` to invoke `ssh`.
- **UX**: Two new top-level commands. No breaking changes to existing commands.
- **Tests**: Testable by injecting a fake client (same pattern as delete);
  the final `exec ssh` call can be tested by capturing the constructed
  command without actually executing it.
