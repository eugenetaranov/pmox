# pmox — next session pickup

## State on disk

- `openspec/config.yaml` — populated with tech stack + tack-mirror conventions
- `openspec/decisions.md` — 5 cross-slice decisions (D-T1..D-T5), all ratified
- `openspec/changes/project-skeleton/` — slice 1, validates clean, 0/59 tasks
- `openspec/changes/configure-and-credstore/` — slice 2, validates clean, 0/59 tasks

Verify with `openspec list` and `openspec validate <name>`.

## Reference

Core context for any agent starting cold:

- **tack = the reference repo**. Layout, Makefile, goreleaser, CI, flag/env patterns, README tone, llms.txt structure all mirror `github.com/tackhq/tack`. Read its `cmd/tack/main.go`, `.goreleaser.yaml`, `Makefile`, `.github/workflows/ci.yaml`, `release.yaml`, and one archived openspec change before touching pmox.
- **Do not implement subcommands in per-file `cmd/pmox/*.go`**. Tack keeps everything in `main.go` except when a command is big enough to justify splitting (tack splits `vault.go`, `export.go`). Slice 2 splits `configure.go` for the same reason. That's the pattern.
- **Project scope is frozen**. Out of scope for v1: LXC, snapshots, multi-VM launch, `pmox shell/exec`, host mounts, non-DHCP networking, Windows builds. See `openspec/config.yaml`.

## Recommended next moves

### Option A — implement slice 1 (`project-skeleton`)
Pure execution. Every decision is settled. Exit explore mode, work through `openspec/changes/project-skeleton/tasks.md` section by section. Result: a buildable `pmox --version` binary with CI + release pipeline ready for a first tag.

**Prereq before first release**: set `HOMEBREW_TAP_TOKEN` secret in the GitHub repo, scoped to write to `eugenetaranov/homebrew-tap`. Task 9.1 in slice 1 calls this out.

**One outstanding placeholder in slice 1**: task 8.1 says "fetch tack's `.golangci.yaml` when this slice is implemented". Do that fetch before writing the pmox lint config — there's no exploration decision needed, just copy tack's file and tweak module paths.

### Option B — draft slice 3 (`server-resolution`) in explore mode
The 5-step precedence rule from D-T4 plus the `-v` log line. Reads the `internal/config` surface produced by slice 2. Should be a small slice (~20 tasks). Open design questions: how does the TTY prompt render the picker, does it reuse the same picker helper as configure's auto-discovery, does the chosen server URL get passed to commands via a context value or a global variable.

### Option C — draft slice 4 (`pveclient-core`) in explore mode
Extends the minimal `internal/pveclient` from slice 2 with the launch-time endpoints (`NextID`, `Clone`, `Resize`, `SetConfig`, `Start`, `AgentNetwork`, `Delete`, `GetStatus`). No real design questions beyond "which endpoints exactly and what's the response shape" — mostly mechanical once you have the PVE API docs open.

## My recommendation

Do **A first** — actually build slice 1 end to end. The act of building will surface things pure thinking can't (goreleaser quirks, lint config gaps, whether tack's 1.21 Go pin in `release.yaml` vs 1.24 in `ci.yaml` matters, whether the interactive Makefile `release` target works on macOS bash vs GNU bash).

Then draft slice 3, then slice 4. Slice 5 (`launch-default`) is where all five T1–T5 decisions actually cash out — don't draft it until at least slice 4 is solid, because launch needs the full `pveclient` to be real.

## Slices still to plan after slice 2

Rough order from the exploration:

3. `server-resolution` — flag → env → single → prompt → error precedence
4. `pveclient-core` — full HTTP client for launch-time endpoints
5. `launch-default` — happy path launch with built-in cloud-init only
6. `list-info-lifecycle` — `list`, `info`, `start`, `stop`, `delete`, `clone`
7. `cloud-init-custom` — `--cloud-init` (full replace only, per D-T5)
8. `post-create-hooks` — `--post-create`, `--tack`, `--ansible`, `--strict-hooks`
9. `docs-and-llms-txt` — real README + llms.txt + examples/

## Parked threads (not yet explored)

- `--strict-hooks` exit code semantics
- What happens if `pmox delete` is interrupted between stop and destroy
- Keychain account-key collision when two pmox installs on one host configure the same URL with different credentials
- The picker helper abstraction (if any) shared between `configure` auto-discovery and `server-resolution` multi-server prompt

None block progress.
