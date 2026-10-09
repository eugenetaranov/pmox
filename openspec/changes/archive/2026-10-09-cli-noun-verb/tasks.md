## 1. Refactor (no behavior change)

- [x] 1.1 Turn `initCmd` + package-global flags into `newInitCmd()` with an `initFlags` struct; update `init_cmd_test.go`
- [x] 1.2 Add `shortcut(root, cmd, canonical)` (annotation, help section, `(canonical)` suffix in Short) and a canonical hint helper
- [x] 1.3 Audit constructors for any other package-level flag binding that would break when instantiated twice; fix

## 2. Groups

- [x] 2.1 `vm` group: launch, clone, list|ls, info, start, stop, delete|rm, shell, exec, cp, sync, apply, ssh-config
- [x] 2.2 `template` group: `create` (existing build), `list|ls` (new: templates with VMID, name, node; `--output json`)
- [x] 2.3 `context` group: list|ls, use, current, rename, delete|rm (reusing the config handlers)
- [x] 2.4 `config` group: edit, path, `cloud-init --regenerate` (reusing runRegenCloudInit)
- [x] 2.5 `mount` group: create, list|ls (new: running daemon mounts from pid/record files, `--output json`), delete|rm; bare `mount <local> <vm>:<path>` falls through to create
- [x] 2.6 Register shortcuts: launch, list|ls, info, start, stop, delete|rm, shell, exec, cp, sync, apply, mount, umount
- [x] 2.7 Command-tree test: every canonical/shortcut/deprecated path resolves via `root.Find`; shortcut↔canonical local flag parity; root PersistentPreRun runs for nested commands

## 3. Help and navigation

- [x] 3.1 Root help sections (Get started / Common / Resources / Maintenance) via cobra groups
- [x] 3.2 Rewrite `rootMenuOptions`: root commands + one entry per noun opening its picker; picks run `SetArgs(strings.Fields(path))`
- [x] 3.3 Bare noun: picker on a terminal; help + exit 2 otherwise (`--no-input`, no TTY); tests

## 4. Deprecations

- [x] 4.1 Deprecated instances (cobra `Deprecated`) for create-template, config *-context, top-level ssh-config and clone
- [x] 4.2 `init --list/--remove/--regen-cloud-init` marked deprecated, delegating to the new handlers
- [x] 4.3 Tests: each deprecated form runs, prints one stderr note, leaves stdout/exit code identical (incl. `--output json`)

## 5. Canonical strings

- [x] 5.1 Replace old-form command strings in Go code (cmd/ + internal/, doctor remediation, error hints) with canonical forms via the helper; update asserting tests
- [x] 5.2 README (command tables, examples, shortcut note, deprecation window), llms.txt, docs/
- [x] 5.3 Check golden files and wizard titles still match

## 6. Verification

- [x] 6.1 `go vet`, `task lint`, `task test`, `openspec validate cli-noun-verb`
- [x] 6.2 Manual: `pmox --help`, `pmox vm --help`, bare `pmox` and `pmox vm` pickers, a deprecated form, `pmox mount list` with a running mount
