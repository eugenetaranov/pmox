## Why

pmox has 24 flat top-level commands, and the access work keeps adding
more. `--help` groups them only visually. A related set like
`create-template`, `config use-context`, `init --list` and
`init --remove` follows no common pattern. Some commands also change
what they do depending on a flag (`init --list`, `--remove`,
`--regen-cloud-init`).

Grouping commands by area as noun-verb (`pmox vm list`,
`pmox template create`) makes the tool discoverable and leaves room to
grow. Keeping the daily commands as top-level shortcuts protects muscle
memory and existing scripts.

## What Changes

- **Noun groups** (singular nouns; `ls` and `rm` are the only verb
  aliases):
  - `pmox vm` — `launch`, `clone`, `list|ls`, `info`, `start`, `stop`,
    `delete|rm`, `shell`, `exec`, `cp`, `sync`, `apply`, `ssh-config`
  - `pmox template` — `create`, `list|ls` (`list` is new)
  - `pmox context` — `list|ls`, `use`, `current`, `rename`,
    `delete|rm`
  - `pmox config` — `edit`, `path`, `cloud-init --regenerate`
  - `pmox mount` — `create`, `list|ls` (new: shows running background
    mounts), `delete|rm`. `pmox mount <local> <vm>:<path>` still works
    as shorthand for `mount create`.
  - `pmox key` and `pmox access` keep their current verbs.
- **Root-level commands that are not shortcuts:** `init`, `doctor`,
  `cleanup`, `version`, `completion`.
- **Top-level shortcuts** for daily use. Each is the same command
  registered a second time, with identical flags, completion, output
  and exit codes:
  - `launch`, `list|ls`, `info`, `start`, `stop`, `delete|rm`
  - `shell`, `exec`, `cp`, `sync`, `apply`
  - `mount` (the noun group itself), `umount` (= `mount rm`)
- **Help** is sectioned into "Get started", "Common", "Resources" and
  "Maintenance". Each shortcut names its canonical form (e.g.
  `(vm list)`).
- **Interactive navigation:**
  - Bare `pmox` and bare nouns (`pmox vm`, `pmox template`, …) open the
    interactive picker on a terminal. The root picker can reach every
    group's verbs.
  - Without a terminal they print help and exit with the user-input
    code.
- **Hints and messages** in errors, doctor remediation and docs always
  use the canonical form.
- **Deprecated forms** keep working for at least two minor releases.
  Each prints a one-line note on stderr (never stdout) pointing at the
  new form. JSON output and exit codes are identical either way.
  - `create-template` → `template create`
  - `config get-contexts|use-context|current-context|rename-context|delete-context`
    → `context list|use|current|rename|delete`
  - `init --list` → `context list`
  - `init --remove <url>` → `context delete <url>`
  - `init --regen-cloud-init` → `config cloud-init --regenerate`
  - `ssh-config` (top level) → `vm ssh-config`
  - `clone` (top level) → `vm clone`
- **BREAKING (after the deprecation window):** the old forms above stop
  working. The window is stated in the release notes and README.

## Capabilities

### New Capabilities

- `cli-command-tree`: the noun-verb command structure, top-level
  shortcuts and their parity with canonical commands, help sections,
  interactive navigation of groups, canonical hints, and the
  deprecation policy for old forms.

### Modified Capabilities

None at the requirement level. Existing capability specs name commands
in their scenarios, but their behavior is unchanged. Commands keep
working under their old names during the deprecation window. Spec text
moves to canonical names when this change is archived.

## Impact

- **`cmd/pmox/main.go`:** registration moves from `addGrouped` to group
  constructors plus a `shortcut()` helper. The root and group pickers
  are rewritten.
- **`cmd/pmox/init.go`:** `initCmd` (a package-level var bound to
  package-global flags) becomes `newInitCmd()` with its own flags
  struct, so it can be registered safely.
- **New group files and constructors:** `vm`, `template`, `context`,
  `mount` (including the new `mount list`), and `config cloud-init`.
- **Command strings:** about 80 old-form strings to update in Go code,
  doctor remediation text, `README.md`, `llms.txt`, `docs/` and
  `openspec/specs`. Tests asserting on hint strings move to canonical
  forms.
- **No changes** to `internal/` logic, config, the API, JSON output or
  exit codes.
