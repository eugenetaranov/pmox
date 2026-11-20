# Tasks

## 1. Config

- [ ] 1.1 Add optional `default_vm` to the server entry in `internal/config` with get/set/clear helpers using the atomic write; tests for round-trip, older config without the field, and two servers with different defaults

## 2. Resolution

- [ ] 2.1 Add options to `vm.Pick` (use default, force pick) implementing rule 0 and the stale-default path; table tests: default in set, default missing (cleared + note), API error (kept), `--pick`, non-terminal
- [ ] 2.2 Mark the default row in the single- and multi-select pickers and start the single-select cursor on it; test the rendered labels and that multi-select pre-selects nothing
- [ ] 2.3 Print the `→ <vm> (default VM)` stderr line when the default decided the target; test it is absent for an explicit name

## 3. Commands

- [ ] 3.1 Add `pmox vm use [name|vmid] [--clear]` and register it in `cmd/pmox/tree.go`; tests for set by name, set by VMID (stores the name), unknown VM, bare without a terminal, `--clear` with and without a default
- [ ] 3.2 Opt in `shell`, `exec`, `vm info`, `start`, `apply`, `vm ssh-config`, `yardo push`, `yardo gitkey` and add `--pick` to each; a command-tree test asserts exactly these carry `--pick` and that `stop`, `delete`, `resize`, `clone` and `access` do not
- [ ] 3.3 Clear the default in `delete` when the deleted VM was the default; test with `--yes`
- [ ] 3.4 Show the `*` marker in `pmox list` and `"default": true` in its JSON; golden test

## 4. Targets

- [ ] 4.1 Resolve a VM-less target (`:`, `:<path>`, mount's bare path) to the default in `cp`, `sync`, `mount`, with `--pick` overriding; tests per command
- [ ] 4.2 Open the interactive `<vm>:<path>` field prefilled with the default VM; test the initial field value and that ↑/↓ still change VM

## 5. Docs

- [ ] 5.1 Document `vm use`, the commands that use the default, the ones that never do, and `--pick` in README ("Interactive selection") and llms.txt, and in each affected command's help; verify `pmox vm use --help` and `pmox shell --help` show it

## 6. Integration

- [ ] 6.1 Run lint and the full test suite and confirm both pass
- [ ] 6.2 On a real cluster with several VMs: `pmox vm use <vm>`, then bare `pmox shell`, `pmox exec -- hostname`, `pmox cp f :/tmp/` go to it; bare `pmox stop` and `pmox delete` still show their picker
