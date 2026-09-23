## 1. tack invocation helper

- [ ] 1.1 Add `internal/tack`: build the `tack run` argv (playbook, `-i <inventory>`, `--hosts <vm>`, optional `--check`/`--tags`/`--skip-tags`/`--auto-approve`/`--output json`)
- [ ] 1.2 Synthesize a temporary `0600` inventory (`hosts: {<vm>: {ssh: {user, key}}}`) and return a cleanup func
- [ ] 1.3 Classify "tack not found on PATH" with a clear install message
- [ ] 1.4 Determine tack's host-key behavior (read tack source / one test); seed the pinned guest key into the inventory if supported, else add a `--ssh-insecure` passthrough; record the outcome for docs
- [ ] 1.5 Unit-test argv building and inventory synthesis (incl. cleanup + perms)

## 2. Per-VM profile memory

- [ ] 2.1 Add `internal/tackprofile`: JSON state under `~/.local/state/pmox/tack/`, keyed by server URL + vmid; Load/Save
- [ ] 2.2 Save only when a profile arg is used; never on explicit `--playbook`
- [ ] 2.3 Tests: remember-then-reuse; `--playbook` leaves memory unchanged

## 3. `pmox apply` command

- [ ] 3.1 Add `cmd/pmox/apply.go`: resolve target (arg optional → picker), auto-start stopped VM + wait for IP (reuse shell/start helpers)
- [ ] 3.2 Implement the playbook resolution ladder (flag → profile arg → remembered → default) with a friendly missing-playbook error pointing at `--init`
- [ ] 3.3 Wire flags: `--playbook`, `--check`, `--tags`/`--skip-tags`, `-y`, `--ssh-insecure`, `--output json`; pass tack stdio through to the terminal
- [ ] 3.4 Register `apply` in `main.go` help grouping (Setup & diagnostics / Access)
- [ ] 3.5 `pmox apply --init` scaffolds `~/.config/pmox/tack/{playbook.yaml,roles/}` without clobbering
- [ ] 3.6 Tests: resolution ladder, `--init` no-clobber, tack-missing error

## 4. Fix + unify the launch `--tack` hook

- [ ] 4.1 Rewrite `hook.TackHook` to use the shared `internal/tack` invocation (`tack run` via synthesized inventory), replacing `tack apply`
- [ ] 4.2 Default `--tack` (no path) to the config playbook
- [ ] 4.3 Update hook tests to the new invocation

## 5. doctor + docs + validation

- [ ] 5.1 Add a doctor check: tack on PATH (warn if absent) + resolvable default playbook
- [ ] 5.2 README + llms.txt: document `pmox apply`, the `~/.config/pmox/tack/` layout, and the resolved host-key behavior
- [ ] 5.3 `openspec validate tack-apply-command --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
