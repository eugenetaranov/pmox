## 1. Shared SSH helper

- [x] 1.1 Add `cmd/pmox/ssh.go` with `sshFlags` struct (`user`, `identity`, `force` fields) and shared flag registration helper.
- [x] 1.2 Implement `resolveSSHTarget(ctx, cmd, client, arg, f)` that: resolves the VM via `vm.Resolve`, runs the tag check (unless `--force`), calls `getOrStartVM` to get the IP, and derives the private key path.
- [x] 1.3 Implement `getOrStartVM(ctx, cmd, client, ref)` that: calls `GetStatus`, if stopped → `Start` + `WaitTask` + `WaitForIP` + `WaitForSSH` with stderr progress, if running → `AgentNetwork` + `PickIPv4`, returns the IP.
- [x] 1.4 Implement `derivePrivateKeyPath(pubkeyPath)` that strips `.pub` and checks the file exists; returns empty string if no config and no flag.
- [x] 1.5 Implement `buildSSHArgs(target, extraArgs)` that assembles the `ssh` argument list: `-o StrictHostKeyChecking=no`, `-o UserKnownHostsFile=/dev/null`, optional `-i`, `user@ip`, plus any extra args.
- [x] 1.6 Add `exec.LookPath("ssh")` check at the top of both commands with a clear error if not found.

## 2. `pmox shell` command

- [x] 2.1 Add `newShellCmd()` in `cmd/pmox/ssh.go` returning a `*cobra.Command` with `Use: "shell <name|vmid>"`, long help mentioning `--user`, `--identity`, `--force`, and auto-start behavior.
- [x] 2.2 Implement `runShell` that calls `resolveSSHTarget`, then `syscall.Exec(sshPath, args, os.Environ())` to replace the process.
- [x] 2.3 Register `newShellCmd()` in `cmd/pmox/main.go`'s `init()`.

## 3. `pmox exec` command

- [x] 3.1 Add `newExecCmd()` in `cmd/pmox/ssh.go` returning a `*cobra.Command` with `Use: "exec <name|vmid> -- <command> [args...]"` and `ArgsFunction` that requires at least one arg.
- [x] 3.2 Implement `runExec` that calls `resolveSSHTarget`, builds the ssh command with `os/exec.Command`, wires stdin/stdout/stderr, runs it, and exits with the remote exit code.
- [x] 3.3 Handle the `--` separator: use cobra's `ArbitraryArgs` and split on `--` in the raw `os.Args` to separate VM name from remote command.
- [x] 3.4 Register `newExecCmd()` in `cmd/pmox/main.go`'s `init()`.

## 4. Tests

- [x] 4.1 Add `cmd/pmox/ssh_test.go` with a `fakeSSHExecer` interface/seam so tests can capture the constructed SSH args without actually exec'ing.
- [x] 4.2 Test `resolveSSHTarget`: tagged running VM → returns IP and derived key path.
- [x] 4.3 Test `resolveSSHTarget`: untagged VM without `--force` → tag error, no API calls beyond resolve.
- [x] 4.4 Test `resolveSSHTarget`: untagged VM with `--force` → proceeds.
- [x] 4.5 Test `getOrStartVM`: stopped VM → Start + WaitTask called, IP returned.
- [x] 4.6 Test `getOrStartVM`: running VM → AgentNetwork called, IP returned.
- [x] 4.7 Test `getOrStartVM`: running VM, guest agent returns no IP → error mentions qemu-guest-agent.
- [x] 4.8 Test `getOrStartVM`: VM not found (404) → error.
- [x] 4.9 Test `buildSSHArgs`: with identity → includes `-i`.
- [x] 4.10 Test `buildSSHArgs`: without identity → no `-i` flag.
- [x] 4.11 Test `buildSSHArgs`: exec mode with extra args → appended after `user@ip`.
- [x] 4.12 Test `derivePrivateKeyPath`: `.pub` path → stripped path returned.
- [x] 4.13 Test `derivePrivateKeyPath`: non-existent derived path → error with guidance.
- [x] 4.14 Test `derivePrivateKeyPath`: empty config, no flag → empty string (no `-i`).

## 5. Documentation + finalize

- [x] 5.1 Update README with `pmox shell` and `pmox exec` sections covering usage, `--user`, `--identity`, `--force`, and auto-start behavior.
- [x] 5.2 Run `go build ./...` and `go test ./...` and confirm green.
- [x] 5.3 Run `golangci-lint run --timeout=3m` and confirm clean.
- [x] 5.4 Commit.
