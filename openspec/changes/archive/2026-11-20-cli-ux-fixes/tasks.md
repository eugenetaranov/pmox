# Tasks

## 1. Global output conventions

- [x] 1.1 Add `-o` to the persistent `--output` flag in `cmd/pmox/main.go`; test that `list -o json` equals `list --output json`, and correct README/llms.txt/help text that mention it
- [x] 1.2 Add a shared colour switch set from `--no-color` / `NO_COLOR` in root `PersistentPreRun`, remove doctor's private check; test that a rendered picker, palette frame and doctor report contain no SGR sequences with either set
- [x] 1.3 Strip the repeated category text from the printed error line in `main`; test `pmox launch` without a terminal prints `Error: missing VM name …` and still exits with the user-input code
- [x] 1.4 Add a usage helper for optional-value flags and apply it to `launch --tack`, `clone --tack`, `apply --default`; test that their help lines contain no `pmox-` sentinel
- [x] 1.5 Use the `context list` name in `pmox info`'s `Context:` line and mark a sole context as current in `context list` (text and JSON); tests for both

## 2. exec without `--`

- [x] 2.1 Change `exec`'s argument split in `cmd/pmox/ssh.go` per design.md; table tests for `web1 uname -a`, `-u root web1 ls -l`, `web1 -A -- cmd`, `-- cmd`, and bare `exec`
- [x] 2.2 Add the "use `pmox exec -- <command>`" hint to the not-found error when no `--` was given; test the message
- [x] 2.3 Update `exec --help`, README and llms.txt examples; verify the documented commands parse in a test

## 3. cleanup

- [x] 3.1 Mark `api-token` destructive, reword its label and detail line; tests: `--apply` skips it, `--include api-token --apply` removes it, checklist row starts unchecked
- [x] 3.2 Generate the category list in `cleanup --help` from the category table and drop the fixed "four" wording; test that every category key appears in help
- [x] 3.3 Count known-hosts pins individually and truncate the list after ten; test with 43 pins
- [x] 3.4 Update the README cleanup section to match; verify by reading it against `pmox cleanup --help`

## 4. list and mount list

- [x] 4.1 Size `pmox list` columns to the widest cell including the overlay label; golden test with a `(tailscale)` row
- [x] 4.2 Reduce `mount list` STATE to one word, print the hint once below the table, keep JSON free of it; tests for stale and all-running

## 5. Integration

- [x] 5.1 Run `task lint` and `task test` (or the Taskfile equivalents) and confirm both pass
- [x] 5.2 Run `pmox info -o json`, `pmox exec <vm> uname -a`, `pmox cleanup` and `pmox mount list` against a real cluster and confirm the output matches the specs
