# Design

## Context

Ten small fixes from the CLI review (see proposal.md). Most are one-file edits; three need a decision.

## Goals / Non-Goals

**Goals:** fix what is wrong without changing any invocation that works today.

**Non-Goals:** renaming or regrouping commands and flags (that is `cli-surface-streamline`); rewriting help prose.

## Decisions

### exec without `--`
Turn off interspersed flag parsing on `exec`, so parsing stops at the first positional (the VM) and everything after it reaches `RunE` untouched. If the remaining arguments contain a literal `--`, the tokens between the VM and `--` are parsed as pmox flags by hand and the rest is the command, which keeps `pmox exec web1 -A -- cmd` working.

- *Alternative: guess whether the first word is a VM.* Rejected: `pmox exec uname -a` would then depend on what VMs exist, and a typo in a VM name would run a command somewhere else.
- Consequence: without `--`, a pmox flag after the VM belongs to the remote command. Help states "flags go before the VM, or use `--`".

### Colour
One switch, set in root `PersistentPreRun` from `--no-color` or `NO_COLOR`, that sets lipgloss's colour profile to ASCII. Everything pmox draws goes through lipgloss or huh, so no per-call-site checks. `doctor`'s own check is replaced by the shared one.

### Optional-value flag help
Cobra prints `NoOptDefVal` verbatim. Keep the sentinel for parsing and override the usage line for those flags with a small helper that renders `--tack [=path]`. Alternative: an empty `NoOptDefVal` is not possible — pflag treats empty as "value required".

### api-token as opt-in
Reuse the existing destructive bit on the category; no new mechanism. The detail line changes from "not the configured token" to "not used by this machine; may be in use elsewhere".

### Error prefix
`exitcode.ErrUserInput` is wrapped with `%w:` at many call sites, which is where the doubled prefix comes from. `main` strips a leading sentinel text when printing; call sites and `errors.Is` checks stay as they are.

## Risks / Trade-offs

- [A script relied on `pmox cleanup --apply` deleting old tokens] → release note; `--include api-token` restores it.
- [exec flag after the VM without `--` changes meaning] → it was a parse error for any flag pmox doesn't know and silently pmox's for the few it does (`-A`, `-u`, `-i`); the latter is the only real change and is called out in help and the release note.
