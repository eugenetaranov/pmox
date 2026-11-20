# Proposal

## Why

Two everyday gaps: pmox generates a completion script, but Tab never completes a VM name, so users retype names they can see in `pmox list`; and there is no way to restart a VM short of `pmox stop` followed by `pmox start`.

## What Changes

- Tab completes VM names (and VMIDs) wherever a command takes `<name|vmid>`, including the VM part of `<vm>:<path>` targets for `cp`, `sync`, `mount`, `umount` and `remount`.
- Tab completes context names (`--context`, `context use|rename|delete`, `config edit`), template names (`launch --template`), and the node, storage and bridge flags of `launch` and `clone`.
- Completion never prompts, never prints errors into the shell, and gives up quietly after a short timeout; results are cached briefly so repeated Tabs don't hit the API each time.
- New `pmox restart [name|vmid ...]` (`pmox vm restart`): stop, then start, then wait for the VM to be reachable, as `start` does. `--hard` powers off instead of shutting down; `--no-wait` and `--wait` match `start`.
- README gains a short "Shell completion" section with the install line for zsh, bash and fish.

## Capabilities

### New Capabilities
- `shell-completion`: what pmox completes dynamically, and how completion behaves when the cluster is slow, unreachable or unconfigured.

### Modified Capabilities
- `list-info-lifecycle`: adds the `restart` command.
- `cli-command-tree`: `vm` gains `restart`, with a top-level shortcut.

## Impact

- New completion helpers in `cmd/pmox` wired into each command constructor (shortcut and canonical instances share them); a small cache file under the pmox state directory.
- New `cmd/pmox/restart.go` reusing the stop and start paths; `cmd/pmox/tree.go`.
- README, `llms.txt`.
- No config or dependency changes.
