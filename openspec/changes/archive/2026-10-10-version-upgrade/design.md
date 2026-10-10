## Context

Release archives are `pmox_<ver>_<os>_<arch>.tar.gz`, with `pmox` at
the archive root, plus a sha256 `checksums.txt`. They're published by
goreleaser to `github.com/eugenetaranov/pmox` for linux/darwin ×
amd64/arm64.

The Homebrew formula `eugenetaranov/tap/pmox` installs into
`<prefix>/Cellar/pmox/<ver>/bin/pmox`, symlinked from `<prefix>/bin`.

Release builds set `main.version` without the leading `v`. `go install`
builds report `dev`; Taskfile builds report `git describe` output.

## Decisions

### D1. Detect the install by the resolved executable path

`os.Executable()`, then `filepath.EvalSymlinks`. A path containing
`/Cellar/pmox/` is Homebrew, on macOS or Linuxbrew. brew is
`<prefix>/bin/brew` next to the Cellar when present, else the `brew`
on PATH. Anything else is a plain binary, `~/go/bin` included.

**Why:** the path is reliable and cheap. Asking brew (`brew list`)
would be slow, and wrong when pmox is also on PATH from elsewhere.

### D2. Our own x.y.z comparison

There's no semver library. Releases are plain `vX.Y.Z`, and
pre-releases are excluded from `latest`. A dev build (`dev`, no
leading digits, or a `-N-g<sha>` describe suffix) is never "up to
date": pmox offers to install the latest release instead.

### D3. Verify before replacing, and replace atomically

**Order:**
1. sha256 check against `checksums.txt`.
2. Extract the tar entry `pmox`, capped at 200 MB.
3. Run `<new> --version` and check that it reports the target version.

**Then replace:**
- **Directory writable:** write `.pmox-upgrade-<pid>` in the same
  directory, then rename it over the executable. The rename is atomic,
  and the running process keeps the old inode.
- **Otherwise:** write a temporary file and run `sudo install -m 0755
  <tmp> <path>`. sudo reads the password from the terminal itself.

**Why:** a failed or interrupted upgrade must never leave a broken or
half-written pmox.

### D4. Homebrew stays in charge of Homebrew installs

pmox doesn't replace files inside the Cellar, because brew would lose
track of the version. It runs `brew upgrade eugenetaranov/tap/pmox`
with the terminal attached and returns brew's exit status. `--version`
doesn't apply here, since brew installs the formula's version.

### D5. Ask first

`tui.Confirm`, default Yes. `--yes` skips it. Without a terminal and
without `--yes` it fails with `ErrUserInput` (exit 2), so a script
never upgrades by surprise.

## Risks / Trade-offs

- [GitHub API rate limit, 60/hour unauthenticated] → `GITHUB_TOKEN`
  is used when set, and a 403 or 429 error says so.
- [A plain binary pmox can't write and sudo isn't available] → The
  error names the path and the manual command.
- [A brew install whose tap hasn't refreshed] → brew's own
  auto-update runs before `upgrade`, as it would by hand.
