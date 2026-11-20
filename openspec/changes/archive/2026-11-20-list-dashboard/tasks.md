# Tasks

## 1. Data

- [x] 1.1 Read `cpu`, `maxcpu`, `mem`, `maxmem`, `maxdisk` into `pveclient.Resource`; extend the pvetest fake to serve them; decode test
- [x] 1.2 Add the usage fields to `vm.Row` with the JSON keys from the spec and fill them in `executeList`; sort running first then by name; JSON golden test
- [x] 1.3 Split the node and storage collection out of `collectHostInfo` so `list` can call it without the version and VM-count calls; `pmox info` output unchanged (existing tests pass)

## 2. Table

- [x] 2.1 Rewrite `vm.RenderTable` with options (terminal, colour, width, wide, multi-node): new columns and cell formats, `ts`/`nb` marker, state glyph, width measured without escape codes; table tests for running, stopped, other state, default marker, overlay, colour alignment
- [x] 2.2 Narrow-terminal column dropping (DISK, UP, MEM, CPU); tests at several widths and for non-terminal output
- [x] 2.3 `--wide` flag, NODE shown automatically with more than one node; tests
- [x] 2.4 Check other callers of `vm.Row` / `RenderTable` (picker, completion) still compile and behave the same

## 3. Frame

- [x] 3.1 Host line per online node (context name, uptime, CPU, memory, largest pool), fetched concurrently with the IP lookups, omitted on error; tests including the node-call failure
- [x] 3.2 Count line with the `--all` pointer; tests for with and without unmanaged VMs, and under `--all`
- [x] 3.3 Print neither line when stdout is not a terminal or the mode is JSON; test the piped line count

## 4. Watch

- [x] 4.1 `--watch`/`-w` and `--interval` flags with validation (terminal required, not with JSON, 1s minimum, `--interval` needs `--watch`); tests for each error
- [x] 4.2 Bubbletea model on the alternate screen reusing the renderer: tick, resize, quit keys, last-good frame plus error line on a failed tick; model tests
- [x] 4.3 Cache IPs across ticks, re-querying only VMs with no address or a changed state; test the number of agent calls over three ticks

## 5. Docs and checks

- [x] 5.1 Update `list` long help and examples, README.md, llms.txt and docs/remote-access.md (new columns, `ts`/`nb`, `--wide`, `--watch`)
- [x] 5.2 `go test -race ./...`, `golangci-lint run`, `openspec validate list-dashboard --strict`
- [x] 5.3 Run `pmox list`, `pmox list --wide`, `pmox list | cat` and `pmox list --watch` against the real cluster and compare with the spec
