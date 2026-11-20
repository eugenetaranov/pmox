# Proposal

## Why

Most days one VM gets nearly all the attention, yet with several VMs on the cluster every bare `pmox shell` stops at a picker. A per-context default VM lets the commands used most go straight to it, while the picker stays for everything else.

## What Changes

- New `pmox vm use [name|vmid]` sets the context's default VM; without a name it shows the picker. `pmox vm use --clear` removes it. Mirrors `pmox context use`.
- The default is stored per context (`default_vm` under the server in `config.yaml`), since a VM name only means something on one cluster.
- With no VM named, these go to the default instead of the picker: `shell`, `exec`, `vm info`, `start`, `apply`, `vm ssh-config`, `yardo push`, `yardo gitkey`, and the VM part of `cp`, `sync` and `mount` targets. It applies with `--no-input` too.
- `stop`, `delete` and everything else that changes or removes a VM ignore the default and keep their picker.
- An explicit name always wins. `--pick` on the commands above shows the picker once, ignoring the default.
- `pmox list` marks the default with `*`; JSON gains `"default": true`.
- A default that no longer exists is reported, cleared, and the command carries on as if none were set. `pmox delete` clears the default when it deletes that VM.
- `pmox launch` does not offer to set a default.

## Capabilities

### New Capabilities
- `default-vm`: setting, storing, showing and clearing a per-context default VM, and which commands use it.

### Modified Capabilities
- `interactive-target-picker`: the shared helper consults the default before its other rules, for commands that opt in.
- `remote-target-input`: the `<vm>:<path>` field and a bare `:` use the default VM.
- `cli-command-tree`: `vm` gains the verb `use`.

## Impact

- `internal/config` (new `default_vm` field), `internal/vm/pick.go`, `cmd/pmox/tree.go`, a new `vm use` command, `list.go` / `internal/vm/table.go`, `delete.go`, `target_prompt.go` / `internal/tui/target`, and the commands listed above (one `--pick` flag each).
- README ("Interactive selection") and `llms.txt`.
- Config stays backward compatible: the field is optional and older pmox versions ignore it.
