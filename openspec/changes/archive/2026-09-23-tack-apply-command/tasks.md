## 1. tack invocation helper

- [x] 1.1 Add `internal/tack`: build the `tack run` argv (playbook, `-c ssh://<user>@<ip>`, `--ssh-key <identity>`, optional `--ssh-insecure`/`--check`/`-t tags`/`--skip-tags`/`--auto-approve`/`--output json`)
- [x] 1.2 Classify "tack not found on PATH" with a clear install message
- [x] 1.3 Host-key behavior confirmed from tack source: verifies against `~/.ssh/known_hosts` fail-closed with `--ssh-insecure` escape; map pmox `--ssh-insecure`/`PMOX_SSH_INSECURE` through (document the `~/.ssh/known_hosts` dependency)
- [x] 1.4 Unit-test argv building (flags, insecure, tags, check, auto-approve)

## 2. Per-VM profile memory

- [x] 2.1 Add `internal/tackprofile`: JSON state under `~/.local/state/pmox/tack/`, keyed by server URL + vmid; Load/Save
- [x] 2.2 Save only when a profile arg is used; never on explicit `--playbook`
- [x] 2.3 Tests: remember-then-reuse; `--playbook` leaves memory unchanged

## 3. `pmox apply` command

- [x] 3.1 Add `cmd/pmox/apply.go`: resolve target (arg optional → picker), auto-start stopped VM + wait for IP (reuse shell/start helpers)
- [x] 3.2 Implement the playbook resolution ladder (flag → profile arg → remembered → default) with a friendly missing-playbook error pointing at `--init`
- [x] 3.3 Wire flags: `--playbook`, `--check`, `--tags`/`--skip-tags`, `-y`, `--ssh-insecure`, `--output json`; pass tack stdio through to the terminal
- [x] 3.4 Register `apply` in `main.go` help grouping (Setup & diagnostics / Access)
- [x] 3.5 `pmox apply --init` scaffolds `~/.config/pmox/tack/{playbook.yaml,roles/}` without clobbering
- [x] 3.6 Tests: resolution ladder, `--init` no-clobber, tack-missing error

## 4. Fix + unify the launch `--tack` hook

- [x] 4.1 Rewrite `hook.TackHook` to use the shared `internal/tack` invocation (`tack run -c ssh://user@ip --ssh-key <id>`), replacing `tack apply`
- [x] 4.2 Default `--tack` (no path) to the config playbook
- [x] 4.3 Update hook tests to the new invocation

## 5. doctor + docs + validation

- [x] 5.1 Add a doctor check: tack on PATH (warn if absent) + resolvable default playbook
- [x] 5.2 README + llms.txt: document `pmox apply`, the `~/.config/pmox/tack/` layout, and the resolved host-key behavior
- [x] 5.3 `openspec validate tack-apply-command --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
