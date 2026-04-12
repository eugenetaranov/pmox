## 1. Shared Infrastructure

- [x] 1.1 Extract `parseRemoteArg(arg string) (vmRef string, remotePath string, isRemote bool)` helper that splits `<name>:<path>` and validates exactly one remote argument
- [x] 1.2 Extract shared SSH-option-building logic so `cp` and `sync` can reuse `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i <key>` construction

## 2. `pmox cp` Command

- [x] 2.1 Add `newCpCmd()` in `cmd/pmox/cp.go` with Cobra wiring: positional args, `--user`, `--identity`, `--force`, `--recursive` flags, and `--` pass-through
- [x] 2.2 Implement `runCp` — parse remote arg, resolve SSH target (reusing `buildSSHClient`/`resolveSSHTarget`/`getOrStartVM`), build scp args, exec via `scp`
- [x] 2.3 Register `newCpCmd()` in `main.go` init
- [x] 2.4 Add table-driven tests for argument parsing (local→VM, VM→local, both-local error, both-remote error)
- [x] 2.5 Add table-driven tests for scp arg construction (with/without key, recursive flag, extra flags via `--`)

## 3. `pmox sync` Command

- [x] 3.1 Add `newSyncCmd()` in `cmd/pmox/cp.go` with Cobra wiring: positional args, `--user`, `--identity`, `--force` flags, and `--` pass-through
- [x] 3.2 Implement `runSync` — parse remote arg, resolve SSH target, build rsync args with `-e 'ssh <opts>'`, exec via `rsync`
- [x] 3.3 Register `newSyncCmd()` in `main.go` init
- [x] 3.4 Add table-driven tests for rsync arg construction (with/without key, extra flags, `-e` flag assembly)

## 4. Verification

- [x] 4.1 Run `go build ./...` and `go vet ./...` clean
- [x] 4.2 Run `golangci-lint run --timeout=3m` clean
- [x] 4.3 Run full test suite passes
