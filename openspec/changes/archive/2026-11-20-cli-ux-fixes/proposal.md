# Proposal

## Why

A review of the whole command surface found a handful of places where pmox contradicts its own help or prints something plainly wrong: `-o json` is advertised but rejected, `pmox exec web1 uname -a` dies on "unknown shorthand flag", `--no-color` only reaches `doctor`, and `pmox cleanup` pre-selects API tokens that other machines still use. None of the fixes changes a working invocation, so they can ship first and on their own.

## What Changes

- `--output` gains the `-o` shorthand that help text already promises.
- `pmox exec <vm> <command…>` works without `--`: the first argument is the VM, the rest is the command, passed through untouched. The `--` form keeps working exactly as today.
- `--no-color` and `NO_COLOR` turn colour off in every command (pickers, palette, spinners, wizard), not only `doctor`.
- Flags with an optional value (`launch --tack`, `clone --tack`, `apply --default`) no longer print their internal sentinel in `--help`.
- A user-input error prints one prefix (`Error: missing VM name …`), not `Error: user input error: …`.
- `pmox info` names the context the same way `pmox context list` does, and `context list` marks the context in use even when it is the only one.
- `pmox list` keeps its columns aligned when an address carries an overlay label such as `(tailscale)`.
- `pmox mount list` shows a one-word STATE and prints the "how to restart / clear it" hint below the table.
- `pmox cleanup`: the `api-token` category becomes opt-in, like `template` and `vm`. A token that is merely absent from this machine's config is not proof that nobody uses it.
- `pmox cleanup`: `--help` lists every category the command checks; the known-hosts group counts pins, not lines.

## Capabilities

### New Capabilities
- `cli-output-conventions`: output rules every command follows — the `-o` shorthand, colour control, the error-line shape, how optional-value flags read in help, and how a context is named.

### Modified Capabilities
- `ssh-shell-exec`: `pmox exec` accepts the remote command without `--`.
- `cleanup`: `api-token` is opt-in; help and counts match what is checked.
- `list-info-lifecycle`: `list` column alignment with overlay labels.
- `mount-command`: `mount list` STATE column and hint placement.

## Impact

- `cmd/pmox/main.go` (flags, error line), `ssh.go` (exec argument split), `cleanup.go`, `list.go` / `internal/vm/table.go`, `hostinfo.go`, `config.go` (context list), `mount.go`, `launch.go`, `clone.go`, `apply.go`, `internal/tui/theme.go` and the palette/wizard styles.
- README and `llms.txt` examples that use `--output json` or `exec … --`.
- No config, API or dependency changes. No existing invocation changes meaning; the one visible default change is `cleanup` no longer pre-checking `api-token`.
