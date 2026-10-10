# self-upgrade Specification

## Purpose
`pmox version upgrade` upgrades pmox in place: through Homebrew for a Homebrew install, otherwise by downloading the release binary, verifying it, and replacing the installed one.
## Requirements
### Requirement: Finding the latest release
`pmox version upgrade` SHALL find the latest release from the GitHub API for `eugenetaranov/pmox`:
- It SHALL use `GITHUB_TOKEN` when that variable is set.
- It SHALL time out after 15 seconds.
- It SHALL show a spinner while it waits.
- `--version vX.Y.Z` SHALL target that release instead of the latest.

It SHALL compare the release with the running version by numeric `major.minor.patch`.

#### Scenario: Up to date
- **WHEN** the running version equals the latest release
- **THEN** pmox prints "✓ pmox <version> is up to date" and exits 0 without downloading anything

#### Scenario: Only checking
- **WHEN** a user runs `pmox version upgrade --check` and a newer release exists
- **THEN** pmox prints the current and new versions and how it would upgrade, installs nothing, and exits 0

#### Scenario: Development build
- **WHEN** the running version is `dev` or a `git describe` string
- **THEN** pmox says the version can't be compared and offers to install the latest release

### Requirement: Install method detection
pmox SHALL resolve its own executable path, following symlinks. It SHALL treat a path containing `/Cellar/pmox/` as a Homebrew install, and anything else as a plain binary.

#### Scenario: Homebrew on macOS
- **WHEN** the resolved path is `/opt/homebrew/Cellar/pmox/0.34.1/bin/pmox`
- **THEN** the method is Homebrew

#### Scenario: Plain binary
- **WHEN** the resolved path is `/usr/local/bin/pmox` or `~/go/bin/pmox`
- **THEN** the method is the binary download

### Requirement: Confirmation
Before upgrading, pmox SHALL ask "Upgrade pmox <current> → <new> via <method>?", defaulting to Yes. `--yes` SHALL skip the question.

Without a terminal and without `--yes`, it SHALL fail with a user-input error (exit 2) that names `--yes`. Declining SHALL exit 130 with nothing changed.

#### Scenario: Script without --yes
- **WHEN** `pmox version upgrade` runs with no terminal and a newer release exists
- **THEN** it exits 2 with an error naming `--yes`, and installs nothing

### Requirement: Homebrew upgrade
For a Homebrew install, pmox SHALL run `brew upgrade eugenetaranov/tap/pmox` with the terminal attached, and SHALL exit with brew's exit status. It SHALL NOT download or replace anything itself.

#### Scenario: Upgrade through Homebrew
- **WHEN** pmox was installed with Homebrew, a newer release exists, and the user confirms
- **THEN** pmox runs `brew upgrade eugenetaranov/tap/pmox` and exits with brew's status

### Requirement: Binary upgrade
For a plain binary, pmox SHALL:
1. download the release's `pmox_<version>_<os>_<arch>.tar.gz` and `checksums.txt`;
2. verify the archive's sha256 against `checksums.txt`;
3. extract the `pmox` binary;
4. run the extracted binary with `--version` and require that it reports the new version;
5. replace its own executable.

It SHALL refuse to replace anything when any of these steps fails.

**Replacing the executable:**
- When the executable's directory is writable, pmox SHALL write the new binary next to it and atomically rename it over the executable, with mode 0755.
- Otherwise, pmox SHALL install it with `sudo install -m 0755`, with the terminal attached so that sudo asks for the password. Without a terminal it SHALL use `sudo -n`, and SHALL explain the failure when a password would be needed.

On success it SHALL print "✓ pmox upgraded <current> → <new> (<path>)" and exit 0.

#### Scenario: Writable directory
- **WHEN** pmox runs from `~/bin/pmox` and the user confirms an upgrade
- **THEN** `~/bin/pmox` is replaced with the new release, has mode 0755, and no sudo is used

#### Scenario: Root-owned directory
- **WHEN** pmox runs from `/usr/local/bin/pmox` as a normal user and the user confirms
- **THEN** pmox runs `sudo install -m 0755 <downloaded> /usr/local/bin/pmox`, and sudo asks for the password

#### Scenario: Checksum mismatch
- **WHEN** the downloaded archive's sha256 doesn't match `checksums.txt`
- **THEN** pmox exits non-zero naming the mismatch, and the installed binary is unchanged

