## Context

`cmd/pmox/mount.go` currently demands `<local_path> <name|vmid>:<remote_path>` for `pmox mount` and `<name|vmid>:<remote_path>` (or `--all <vm>`) for `pmox umount`. Both commands call `parseRemoteArg` and hard-fail if the `<name|vmid>:` prefix is missing.

The shared picker helper `resolveTargetArg(ctx, client, args, stderr)` lives in `cmd/pmox/delete.go` and is already used by `shell`, `exec`, `start`, `stop`, `info`, and `delete`. It handles the TTY detection, auto-select-single, picker UI, and the non-interactive / zero-VM error paths. This change adopts that same helper from `mount.go` without modifying it.

## Goals / Non-Goals

**Goals:**
- Make `<name|vmid>:` optional in `pmox mount <local> [<name|vmid>:]<remote>`.
- Make `pmox umount` callable with no positional arguments at all, in which case it resolves the VM via the picker and stops every mount for that VM.
- Reuse the existing `resolveTargetArg` helper — no new picker code, no changes to `interactive-target-picker`.
- Keep every existing explicit-form invocation byte-identical on the wire (same rsync args, same PID files, same signals).
- Update `--help` / long descriptions for both commands.

**Non-Goals:**
- Changing the two-positional shape of `mount` (`<local> <remote>`). A zero-arg `pmox mount` is out of scope — the local path is still required.
- Changing how excludes, `--force`, the tag check, daemon mode, or the rsync command line work.
- Adding a new picker for the `<remote_path>` itself or remembering previous mount destinations.
- Touching `cp` / `sync` destination parsing.

## Decisions

### Decision 1: Detect "no VM specified" by the absence of a `<name|vmid>:` prefix in the destination arg

**Choice:** In `runMount`, parse the second positional with `parseRemoteArg`. If `isRemote` is false, treat the raw arg as the remote path, set `ref = ""`, and call `resolveTargetArg(ctx, client, nil, stderr)` to obtain the VM name via the picker. Then proceed as today.

**Alternatives considered:**
- *Require `:<path>` as a leading-colon sentinel* (e.g. `pmox mount ./src :/opt/app`). Rejected — uglier, harder to teach, and `parseRemoteArg` would need changes.
- *Add a `--vm` flag that overrides the destination parsing*. Rejected — redundant with the existing `<name>:<path>` syntax and inconsistent with how every other pmox command handles targets.
- *Make the second positional optional and default the remote path to `~`*. Rejected — surprising, and the user's message explicitly asks for "no vm name", not "no remote path".

**Rationale:** `parseRemoteArg` already returns `isRemote=false` for any arg that doesn't match `<name|vmid>:<path>`. Reusing that signal keeps the parser untouched and the UX consistent with what users already type — `:` means "here comes the VM".

### Decision 2: `pmox umount` with zero args picks a VM and stops all its mounts

**Choice:** Change `umount`'s arg validation from `cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)`. When `len(args) == 0`:
1. Build the SSH client and call `resolveTargetArg(ctx, client, nil, stderr)` to get the VM.
2. Delegate to the existing `umountAll(cmd, vmName)` path, which already iterates PID files by VM prefix and SIGTERMs each.

When `len(args) == 1`, the existing behavior is preserved verbatim (both the specific `name:path` form and `--all name` form still work).

**Alternatives considered:**
- *Keep `--all` as the only way to stop multiple mounts* and have bare `pmox umount` error. Rejected — the user explicitly asked for bare umount to "simply unmount all mounts" when there is one VM; `--all` plus a picker-resolved name is what that desugars to.
- *Treat bare `umount` as "stop every mount across every VM"*. Rejected — the user's request ties the behavior to the single-VM case, and the picker handles the multi-VM case naturally.

**Rationale:** The existing `umountAll` helper already does the right thing once a VM name is known. The change is a two-line routing fix in `runUmount` plus the arg relaxation.

### Decision 3: Multi-VM + TTY falls through to the shared picker; non-TTY keeps today's error

**Choice:** `resolveTargetArg` already implements the full matrix (zero VMs → error, one VM → auto-pick, multi + TTY → picker, multi + non-TTY → error). Call it and let it own the behavior. No `mount`-specific branching.

**Rationale:** Consistency with `shell`, `exec`, `delete`. Any future changes to picker UX automatically apply to `mount`/`umount`.

### Decision 4: Update both `--help` strings; add one bare-path example to `mount` long description and one bare example to `umount`

**Choice:** Edit the `Long:` fields in `newMountCmd` and `newUmountCmd` to mention the optional `<name|vmid>:` prefix and show:
- `pmox mount ./src /opt/app` (picker)
- `pmox umount` (picker, stops all mounts for the picked VM)

**Rationale:** The user explicitly asked to "update help too". Keep the edits minimal — a single example line each and one sentence explaining the fallback.

## Risks / Trade-offs

- **[Risk] A user types `pmox mount ./src /opt/app` expecting that to be picked up as a two-path local copy.**
  → Mitigation: pmox has no local-to-local sync, and the error path (picker runs, zero VMs → clear message) surfaces the VM resolution explicitly. Documented in the updated `--help`.

- **[Risk] `pmox umount` with no args silently stops mounts the user forgot about.**
  → Mitigation: the behavior is gated on "exactly one pmox VM" (auto-select) or on an explicit TTY picker choice. In both cases the existing `umountAll` logs each stopped mount to stderr with its PID. A curious user can still run `pmox umount --all <vm>` today and see the same output.

- **[Risk] A script that today runs `pmox umount web1:/opt/app` starts misbehaving because arg validation loosens from `ExactArgs(1)` to `MaximumNArgs(1)`.**
  → Mitigation: existing one-arg invocations take the same code path they do today; `len(args) == 0` is the only new branch. Covered by a regression test asserting that `pmox umount web1:/opt/app` is unchanged.

- **[Trade-off] `mount` still requires the local source path.**
  → Accepted: pmox can't meaningfully guess the source. A fully zero-arg `pmox mount` would be a different, larger change.

## Migration Plan

No migration. The change is purely additive at the CLI boundary — all existing invocations continue to work. No config changes, no PID-file format changes, no rsync command-line changes.
