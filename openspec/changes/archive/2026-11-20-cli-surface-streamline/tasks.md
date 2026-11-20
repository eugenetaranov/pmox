# Tasks

## 1. Deprecation plumbing

- [x] 1.1 Add `deprecatedFlag(cmd, old, new)` in `cmd/pmox/tree.go` (hidden flag, one stderr note, value forwarded); tests: note on stderr only, stdout identical, flag absent from help and completion

## 2. `--hard` / `--force`

- [x] 2.1 Add `stop --hard`, turn `stop --force` into a deprecated flag for it; update `restart` and `delete` help to the same wording; tests for both spellings against the fake PVE server
- [x] 2.2 Add a command-tree test asserting `--hard` and `--force` descriptions match the two allowed meanings on every command; update README and llms.txt mentions of `stop --force`

## 3. `--context` only

- [x] 3.1 Let `--context` / `PMOX_CONTEXT` take a name or URL through the existing canonicalization; tests for name, bare host, full URL, unknown value
- [x] 3.2 Deprecate `--server` and `PMOX_SERVER` (hidden, note, conflict error when both name different servers); tests for each, and that `--help` no longer lists `--server`
- [x] 3.3 Replace `--server` with `--context` in README, docs/ and llms.txt; verify with a grep that no documented example uses `--server`

## 4. resize

- [x] 4.1 Add the shared float-GiB memory parser and use it for `launch --mem`, `clone --mem` and the launch memory prompt; tests for `1.5`, `4`, `0`, `lots`, and that launch JSON still reports MiB
- [x] 4.2 Rewrite `vm resize` as `[name|vmid] --cpu --mem --disk [--restart|--no-restart]` with validate-first ordering and a single restart; tests: combined change, no flags, unchanged value, shrink refused with nothing changed, disk + cpu with one restart, non-terminal pending note
- [x] 4.3 Keep `vm resize cpu|ram|disk` as deprecated commands calling the new path; tests that each old form matches its flag form and prints the note
- [x] 4.4 Add the root `resize` shortcut (Common section); the parity test covers it; update README tables and llms.txt

## 5. Root info

- [x] 5.1 Make root `info` take `[name|vmid]`: VM report with an argument, host report without; tests for bare (no picker with several VMs), with a VM (equals `vm info`, text and JSON), unknown VM (not-found, not "unknown command")
- [x] 5.2 Update `info --help`, `vm --help` and the README "pmox info" section; verify the examples run

## 6. playbook group

- [x] 6.1 Add `pmox playbook init [url]`, `list`, `default [name]`, `update` in `cmd/pmox/playbooks.go` over the existing functions and register the group; tests for each scenario in `playbook-management`
- [x] 6.2 Deprecate `apply --init`, `--default`, `--update` via `deprecatedFlag`, and rename the second argument to `playbook` in usage, help and errors; tests for old flags and the usage line
- [x] 6.3 Update README, examples/README.md and llms.txt to the `playbook` commands; verify `pmox playbook --help` and `pmox apply --help` read consistently

## 7. Smaller renames

- [x] 7.1 Add `context edit`, deprecate `config edit`; tests for both; update hints that name `config edit` (init's "already set up" screen, doctor)
- [x] 7.2 Make `router route delete` canonical with alias `rm`; test help and both spellings
- [x] 7.3 Add `access revoke --from`, deprecate `--to` on revoke, error when both are given; tests; update README sharing section
- [x] 7.4 Add `cleanup -y/--yes` as `--apply` (not `PMOX_ASSUME_YES`); deprecate `--include-templates` / `--include-vms`; tests; update README cleanup examples
- [x] 7.5 Hide `remount` from root help and the root palette without a deprecation note; tests that it still runs and is not listed
- [x] 7.6 Extend the README "Renamed commands" table with every form deprecated in this change; verify each row against a run of the old form

## 8. Confirmation

- [x] 8.1 Add `tui.ConfirmLine` and switch every command's yes/no question to it (`resize`, picked `context delete`, `upgrade`, `key publish --replace`, playbook backup, missing remote directory); tests for the prompt text, defaults and end of input
- [x] 8.2 Add a test that no command file outside the wizards calls the button-style confirm

## 9. Palette search

- [x] 9.1 Add a flattened, de-duplicated command list to the palette resolver and rank own-level matches first; model tests: `autostart` at root finds `mount autostart`, `list` at root appears once, empty filter shows one level, Enter on a deep match returns its full path

## 10. Help text

- [x] 10.1 Add the usage template that replaces the Global Flags block on subcommands with a pointer line; test `shell --help` and root `--help`
- [x] 10.2 Move examples into the `Example` field for every runnable command and add missing ones; command-tree test that every runnable command has one
- [x] 10.3 Rewrite `Long` text per the conventions (current behaviour only, no endpoints or limits, no retired names), moving cut detail to README/docs where it is still useful; command-tree test for the banned phrases
- [x] 10.4 Regenerate or hand-update llms.txt from the new help; verify its command list matches `pmox --help`

## 11. Integration

- [x] 11.1 Update the command-tree test's expected table to the one in `specs/cli-command-tree`; run lint and the full test suite and confirm both pass
- [x] 11.2 Run every deprecated form from the README table against a real cluster or the fake server and confirm identical stdout and exit code plus one stderr note
