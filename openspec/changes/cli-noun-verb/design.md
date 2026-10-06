## Context

`cmd/pmox/main.go` registers every command at root through
`addGrouped(group, cmds…)`. Groups only affect how `--help` is
sectioned.

- **Constructors:** nearly every command comes from a `newXxxCmd()`
  constructor whose flags are local to the closure, so building a
  second instance is safe. The exception is `initCmd`: a package-level
  `*cobra.Command` whose `--list`, `--remove` and `--regen-cloud-init`
  flags bind to package globals.
- **Root picker:** the bare-`pmox` picker (`rootMenuOptions`) lists
  single-word root commands and re-runs with `SetArgs([]string{name})`.
- **Hints:** every hint ("run 'pmox access sync'") is a literal string.
  Nothing uses `CommandPath()`.

Inputs: a review panel (CLI UX, homelab admin, maintainer). The user
then decided:

- the daily set of shortcuts (about 12);
- `mount` as a noun;
- the kubectl-style `config *-context` forms are deprecated like
  everything else;
- `init` stays the setup command.

## Goals / Non-Goals

**Goals:**

- One canonical noun-verb path per operation.
- Daily commands stay one word long.
- Shortcuts are exactly as capable as their canonical form: same flags,
  completion, output and exit codes.
- Old forms keep working, with a clear note on stderr, for at least two
  minor releases.

**Non-Goals:**

- Behavior changes to any command (except the new `template list` and
  `mount list`).
- New global flags (`-q`, JSON schema versioning).
- Completion caching.
- Plural noun aliases.

## Decisions

### D1. Shortcuts = the same constructor registered twice

For example:

```go
vm.AddCommand(newListCmd())
shortcut(root, newListCmd(), "vm list")
```

`shortcut()` does three things:

- sets `Annotations["pmox.canonical"]`;
- places the command in the help section the table below assigns it
  ("Get started" or "Common");
- appends `(vm list)` to its `Short`.

Each instance owns its flags, `Args` validators and `ValidArgsFunction`.

- *Rejected: cobra `Aliases`.* They only match at the same level, so
  they can't map `pmox list` to `vm list`.
- *Rejected: thin wrappers that call `SetArgs` and re-execute.* They
  lose flag definitions and completion.
- *Safety net:* a table test resolves every canonical and shortcut path
  with `root.Find`. It asserts each pair has identical local flag sets
  (names, shorthands, defaults), and that each deprecated path resolves.

### D2. `init` gets a constructor

`initCmd` and its globals become `newInitCmd()` with an `initFlags`
struct. `init_cmd_test.go` drives the flags through the command instead
of mutating globals.

The deprecated `--list`, `--remove` and `--regen-cloud-init` flags stay
and are marked deprecated with `MarkDeprecated`. Each one still runs
the same code as its replacement.

### D3. Deprecated forms: hidden, still working, note on stderr

Old command names (`create-template`, `config get-contexts`, …) are
fresh constructor instances wrapped by `deprecated(c, replacement)`.
The wrapper:

- marks the command Hidden, so it is not in help or completion;
- records the replacement in an annotation;
- writes one note, naming the replacement, to `cmd.ErrOrStderr()`
  before running;
- leaves stdout and exit codes unchanged.

The deprecated `init` flags are hidden and print the same kind of note.

- *Why not cobra's `Deprecated` / `MarkDeprecated`:* the command-tree
  test showed cobra writes those notes to the command's *output*
  writer, which can be stdout, and that corrupts `--output json`.

The window is at least two minor releases. After that, each becomes a
hard-error stub (the existing `deprecatedConfigureCmd` pattern), then
is removed.

### D4. Canonical hints

Every hint, doctor remediation and error message names the canonical
form directly. A one-time sweep replaced the old forms in `cmd/`,
`internal/`, the docs and the tests that assert them. A helper
indirection was considered, but literal canonical strings proved
clearer to read and grep.

### D5. Mount as a noun that still accepts the old shape

`pmox mount` is a group with `create`, `list` and `delete|rm`. When its
first argument isn't a subcommand name, its own `RunE` treats the
arguments as `mount create`. That keeps `pmox mount ./src web1:/app`
working.

- **Ambiguity:** a local directory literally named `list`, `ls`,
  `create`, `rm` or `delete` needs the `./list` form. This is
  documented in the command's help.
- **`mount list`** reads the existing daemon pid/record files (the same
  source `umount` uses) and prints VM, remote path, local path, PID and
  started-at. It supports `--output json`.
- **`umount <vm>`** remains as a shortcut for `mount rm <vm>`.

### D6. Help layout and pickers

Root help is sectioned into groups:

| Section | Commands |
|---|---|
| Get started | `init`, `launch`, `shell` |
| Common | `list`, `info`, `start`, `stop`, `delete`, `exec`, `cp`, `sync`, `apply`, `mount`, `umount` |
| Resources | `vm`, `template`, `context`, `config`, `key`, `access` |
| Maintenance | `doctor`, `cleanup`, `version`, `completion` |

`rootMenuOptions` changes in two ways:

- It lists the root's shortcuts and init/doctor/cleanup, plus one entry
  per noun group that opens that group's picker.
- Picks run with `SetArgs(strings.Fields(path))`.

A bare noun on a terminal shows its verbs. With `--no-input` or no
terminal, it prints help and exits 2.

### D7. Canonical names everywhere

`README.md`, `llms.txt`, `docs/` and doctor remediation use canonical
forms, and mention shortcuts once. Tests that assert hint strings are
updated in the same commit as the hints.

## Risks / Trade-offs

- **[A flag added to only one instance]** → the flag-parity test (D1).
- **[Longer help]** → sections, and every shortcut shows its canonical
  form, so it stays readable.
- **[Scripts that parse stderr see deprecation lines]** → one line
  only, never on stdout, exit codes unchanged.
- **[Group-level `PersistentPreRun` shadowing root's]** → groups define
  none. A test asserts root's pre-run fires for `pmox vm list`.
- **[Picker regressions]** → unit tests on menu construction. The
  interactive path is covered by an end-to-end flow where practical.

## Migration Plan

1. Refactor only: `newInitCmd()`, the `shortcut()` helper, the hint
   helper. The suite stays green.
2. Add the groups alongside the flat commands, plus the parity test.
3. Switch the old names to deprecated instances and keep the chosen
   shortcuts. Update hints, docs and the tests that assert them.
4. Release notes announce the deprecation window.
5. Two minor releases later, convert the old forms to hard-error stubs.

## Open Questions

None. The panel's open items were decided by the user.
