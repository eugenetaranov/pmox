## 1. `pmox mount` optional target

- [x] 1.1 In `cmd/pmox/mount.go`, change `runMount` so that when `parseRemoteArg(args[1])` returns `isRemote == false`, it calls `resolveTargetArg(ctx, client, nil, cmd.ErrOrStderr())` to obtain the VM name and uses the raw `args[1]` as the remote path
- [x] 1.2 Keep the explicit `<name|vmid>:<remote_path>` path unchanged (no picker call, no extra `ClusterResources` fetch)
- [x] 1.3 Update `newMountCmd` `Short` / `Long` / `Use` to document the optional `<name|vmid>:` prefix, add the bare `pmox mount ./src /opt/app` example, and keep the existing examples
- [x] 1.4 Make sure daemon mode (`-D`) propagates the resolved VM name into the child `pmox mount` invocation so the child does not re-run the picker (pass the `<vm>:<remote>` form in `childArgs`)

## 2. `pmox umount` zero-arg form

- [x] 2.1 Relax `newUmountCmd` arg validation from `cobra.ExactArgs(1)` to `cobra.MaximumNArgs(1)` and update the `Use` / `Short` / `Long` strings
- [x] 2.2 In `runUmount`, when `len(args) == 0`, build the SSH client, call `resolveTargetArg(ctx, client, nil, cmd.ErrOrStderr())`, and route to `umountAll(cmd, resolvedVM)`
- [x] 2.3 When `len(args) == 1`, preserve today's behavior verbatim (both `<name|vmid>:<path>` and `--all <name|vmid>` forms)
- [x] 2.4 Add a bare `pmox umount` example to the help text

## 3. Tests

- [x] 3.1 Unit test: `runMount` with a bare remote path invokes the picker helper (via a test double for `resolveTargetArgFn` or equivalent indirection) and passes the raw path through as the remote path
- [x] 3.2 Unit test: `runMount` with an explicit `<name>:<path>` does NOT invoke the picker helper
- [x] 3.3 Unit test: `runUmount` with no args invokes the picker helper and then calls the umount-all code path for the resolved VM
- [x] 3.4 Regression test: `runUmount` with an explicit `web1:/opt/app` still routes to `umountByRemote` exactly as before
- [x] 3.5 Regression test: `runUmount --all web1` still routes to `umountAll` exactly as before
- [x] 3.6 Help-text assertion: `pmox mount --help` and `pmox umount --help` include the new bare-form examples

## 4. Docs + spec sync

- [x] 4.1 Update `README.md` (and `docs/llms.txt` if it covers mount/umount) to mention the optional `<name|vmid>:` prefix on `pmox mount` and the zero-arg `pmox umount` form
- [x] 4.2 Run `go build ./...` and `go test ./...` clean
- [x] 4.3 Run `golangci-lint run ./...` clean
- [ ] 4.4 Manual smoke test: `pmox mount ./src /tmp/foo` with one VM auto-selects; `pmox umount` with one VM stops all of its mounts
