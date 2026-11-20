# Tasks

## 1. Completion plumbing

- [x] 1.1 Add the restricted completion client (2 s budget, no input, no pinning or config writes, silent on error); tests with an unreachable server and with no config return no candidates and no stderr output
- [x] 1.2 Add the per-context completion cache with a 10 s TTL and invalidation helper; tests for hit, expiry and invalidation

## 2. Completers

- [x] 2.1 Add `completeVMs` (state filter, exclude already-typed, VMID on digits, descriptions) and wire it into every `<name|vmid>` command; a command-tree test asserts each such command has a `ValidArgsFunction`, and `__complete` tests cover `shell w`, `stop web1 `, `start `
- [x] 2.2 Add `completeVMTargets` for `cp`, `sync`, `mount` (name + `:`, no trailing space, file completion kept) and mount-record completion for `umount` / `remount`; `__complete` tests for each
- [x] 2.3 Add context, template, node, storage and bridge completers and register them on `--context`, `context use|rename|delete`, `config edit`, `launch`/`clone` flags; `__complete` tests for each
- [x] 2.4 Verify hidden/deprecated commands and flags are not offered; test `__complete create-`
- [x] 2.5 Invalidate the cache after successful `launch`, `clone`, `delete`, `start`, `stop`; test that a completion after `delete` no longer offers the VM
- [x] 2.6 Add a "Shell completion" section to README and llms.txt with zsh/bash/fish install lines and what completes; verify the documented install line works in zsh

## 3. Restart

- [x] 3.1 Add `cmd/pmox/restart.go` composing the stop and start paths with `--hard`, `--no-wait`, `--wait`; tests with the fake PVE server for running, stopped, hard, shutdown-timeout (suggests `--hard`), and several VMs with one failure
- [x] 3.2 Register `vm restart` and the root shortcut in `cmd/pmox/tree.go` (Common section), with completion and cache invalidation; the existing shortcut-parity test covers it
- [x] 3.3 Document `restart` in README command tables and llms.txt; verify `pmox restart --help` shows an example

## 4. Integration

- [x] 4.1 Run lint and the full test suite and confirm both pass
- [x] 4.2 Against the real cluster, `pmox __complete shell ""`, `cp f ""`, `--context ""` and `launch x --template ""` return the expected names (0.4 s cold, under 0.1 s cached); an unresolvable context returns nothing
- [x] 4.3 Verify restart end to end against the fake PVE server (stop, start, IP wait); a run against the real cluster is left to the user, since it would restart a live VM
