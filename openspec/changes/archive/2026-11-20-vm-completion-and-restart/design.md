# Design

## Context

Cobra already generates the completion scripts and calls back into the binary (`__complete`) for dynamic values; pmox registers no `ValidArgsFunction` or flag completion today. Shortcuts and canonical commands are separate instances of the same constructor, so anything set in the constructor reaches both. See proposal.md for motivation.

## Goals / Non-Goals

**Goals:** completion that is never slower than about two seconds and never noisy; restart built from the existing stop and start paths.

**Non-Goals:** completing remote paths after `<vm>:` from the shell (the interactive target field already does this over SSH; from a completion callback it would mean an SSH round-trip per Tab); completing tack playbook or yardo profile names; a guest-side reboot.

## Decisions

### One completion helper per kind
`completeVMs(filter)`, `completeContexts`, `completeTemplates`, `completeClusterNames(kind)`, `completeVMTargets` (appends `:` and returns `ShellCompDirectiveNoSpace`). Each constructor sets `ValidArgsFunction` / registers flag completion with the matching helper. A command-tree test asserts every command whose `Use` contains `[name|vmid` has a `ValidArgsFunction`, so new commands can't forget it.

### Completion runs in a restricted mode
The `__complete` entry point builds its client with a 2 s context, `tui.SetNoInput(true)`, and the read-only host-key/TLS path `doctor` uses (no pinning prompts, no config writes). Any error returns `ShellCompDirectiveNoFileComp` with no candidates; nothing is written to stderr unless `BASH_COMP_DEBUG_FILE` is set (cobra's own debug hook).

### Cache
A JSON file per context under the state directory (`~/.local/state/pmox/completion/<slug>.json`) holding the last cluster-resources answer and a timestamp; 10 s TTL. It makes Tab-Tab and prefix refinement free and bounds API calls. `launch`, `delete`, `start`, `stop` and `restart` remove it on success so names never look stale after the user's own action.
- *Alternative: no cache.* Each Tab is one API call (~100–300 ms on a LAN, far worse over an overlay). Rejected.
- *Alternative: long TTL.* Rejected: stale names after work in the web UI.

### Restart is stop + start
Reuse `stop`'s shutdown/hard-stop and task wait, then `start`'s start-and-wait, per VM in sequence. PVE's `reboot` endpoint is not used: it needs a responsive guest agent or ACPI and, more to the point, a guest-initiated reboot does not apply pending config, which is the reason `resize` already does stop-then-start.
- Sequential rather than parallel: simpler output, and restarting several VMs at once is rare.
- `--hard` rather than `--force`, matching the flag meaning `cli-surface-streamline` settles on, so the new command never has the old ambiguity.

## Risks / Trade-offs

- [Completion offers a name deleted seconds ago in the web UI] → 10 s TTL; the command itself still reports not-found.
- [Completion over an overlay link is slow] → 2 s cap, then nothing; the cache covers repeats.
- [Users must reinstall nothing] → dynamic completion works with the already-installed script, since the script only calls `pmox __complete`.
