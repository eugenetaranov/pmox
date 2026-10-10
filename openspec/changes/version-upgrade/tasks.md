## 1. Command

- [x] 1.1 `cmd/pmox/upgrade.go`: `pmox version upgrade` with `--check`, `--yes`, `--version`; latest release lookup (GITHUB_TOKEN, 15s, spinner); version parse/compare with dev builds
- [x] 1.2 Install detection from the resolved executable (`/Cellar/pmox/` → Homebrew, else binary); brew path next to the Cellar or on PATH
- [x] 1.3 Confirmation (default Yes, `--yes`, non-interactive error naming `--yes`)
- [x] 1.4 Homebrew route: `brew upgrade eugenetaranov/tap/pmox`, terminal attached, brew's exit status
- [x] 1.5 Binary route: download archive + checksums.txt, sha256 verify, extract, run `--version`, atomic rename or `sudo install -m 0755` (`sudo -n` without a terminal)
- [x] 1.6 `newVersionCmd` (palette leaf: picking it prints the version, no submenu) gains the `upgrade` subcommand and writes through `cmd.OutOrStdout()` with unchanged text

## 2. Tests and docs

- [x] 2.1 Unit tests: compare, detection, checksum, extraction; end-to-end against an httptest release (writable replace, mismatch, up to date, --check, non-TTY, brew command, sudo command)
- [x] 2.2 README install section + llms.txt
- [x] 2.3 `go vet`, `task lint`, `go test -race ./...`, `openspec validate version-upgrade`
- [x] 2.4 Real runs: --check on this Mac (Homebrew); binary route on a VM in a root-owned and a writable directory; --yes without a terminal
  - Done against the real v0.35.0 release:
    - On the Mac, a 0.34.0 build in a writable directory upgraded itself; the binary became the release (commit 56fed8b, mode 0755).
    - A 0.34.0 build under a `Cellar/pmox/` path prompted "Upgrade pmox 0.34.0 → 0.35.0 via Homebrew?", then ran `brew upgrade eugenetaranov/tap/pmox` ("already installed"), and exited 0.
    - On an Ubuntu VM, a user whose sudo needs a password upgraded `/usr/local/bin/pmox` (root-owned): confirm, "needs sudo", sudo's password prompt, result 0.35.0, root 0755.
    - The same user without a terminal got `sudo -n`'s refusal, an error pointing to the terminal, and an unchanged binary.
