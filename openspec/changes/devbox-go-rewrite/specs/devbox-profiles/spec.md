## ADDED Requirements

### Requirement: Profile format
A profile SHALL be a TOML file with `picks`, and optionally `mise_extra`, `[git]` (`name`, `email`), `[system]` (`tz`) and `[mcp]` (`servers`, `listen`, `wire_agents`). It SHALL NOT contain commands, steps or secrets. A profile with an unknown key SHALL be rejected, naming the key. Unknown pick ids SHALL be dropped with a warning.

#### Scenario: Typo in a key
- **WHEN** a profile has `pick = ["cli"]`
- **THEN** devbox-setup exits non-zero naming `pick`

#### Scenario: Old id
- **WHEN** a profile lists `shell`
- **THEN** it is read as `zsh zshcfg p10k`, using the saved-answer migrations

### Requirement: Export and load
`devbox-setup export [file]` SHALL write the saved answers as a profile (stdout by default). `devbox-setup --profile <file|https URL>` SHALL load a profile into the review screen with its items ticked. With `--yes`, it SHALL apply the profile without prompting. A URL SHALL use https.

#### Scenario: Round trip
- **WHEN** a profile is exported on one VM and applied with `--profile f.toml --yes` on a fresh VM
- **THEN** the second VM's saved answers equal the first VM's

### Requirement: Profile at launch
`pmox launch --devbox-profile <file>`, or the server config's `devbox_profile`, SHALL copy the profile to the VM with devbox-setup and run `sudo devbox-setup --profile <path> --yes`, streaming its output. A failure SHALL be a warning, or a launch failure with `--strict-hooks`.

#### Scenario: Launch with profile
- **WHEN** `pmox launch web1 --devbox-profile team.toml` runs
- **THEN** after launch, the profile's items are installed and saved as web1's answers
