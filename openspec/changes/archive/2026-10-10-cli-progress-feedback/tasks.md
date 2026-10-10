## 1. Spinner

- [x] 1.1 `internal/tui.Spinner`: delayed first frame (`SpinnerDelay`), in-place `Set`, `Stop` clears only if drawn, `Succeed` result line, nil-safe, one active spinner at a time (nested `StartSpinner` returns nil), `Writer` for notes printed while it runs
- [x] 1.2 `main` turns spinners off for `-v`/`--debug`; `stepSpinner` (launch/clone/delete/template progress) rebuilt on `tui.Spinner`
- [x] 1.3 `startSpin` / `finishSpin` / `doneMark` helpers in `cmd/pmox/spinner.go`; result lines stay on stdout

## 2. Commands

- [x] 2.1 start, stop: one relabelled spinner; "already running" / "already stopped" are success (status pre-check plus the Proxmox task error for the race)
- [x] 2.2 list, info, ssh-config, template list, the VM picker's "Loading VMs…"
- [x] 2.3 delete: lookup before the confirmation, status check, snippet removal; "already gone" is a ✓ line
- [x] 2.4 shell, exec, cp, sync, mount: one spinner through resolve → start → IP → SSH; still-booting wait
- [x] 2.5 apply: lookup, connect, host-key pin, tack roles fetch
- [x] 2.6 launch, clone: snippet storage check as a step, template lookups, shared access after launch; hook runs without a spinner (it streams output)
- [x] 2.7 access grant/revoke/sync/show/setup, key publish/unpublish/show/list; nothing to revoke and nothing to unpublish are success
- [x] 2.8 doctor check pass, cleanup scan and removals, config edit's connection check, template image URL resolve and old-image pruning
- [x] 2.9 umount: stopping a mount; nothing mounted is success; a mount already running is success

## 3. Verification

- [x] 3.1 Unit tests: no flash for fast ops, labels and clearing, result line, nil spinner, nested spinner; start/stop/umount/mount/key/access "already" cases
- [x] 3.2 `go vet`, `task lint`, `go test -race ./...`
- [x] 3.3 Real cluster through a pty: start on a running VM (✓, exit 0, plain line when piped), launch and delete show step spinners, fast steps draw nothing
