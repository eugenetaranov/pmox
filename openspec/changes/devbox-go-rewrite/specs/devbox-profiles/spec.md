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

### Requirement: Layered profiles
`--profile` SHALL be repeatable. Profiles SHALL be applied in order. For `[git]`, `[system]` and `user`, a later profile's value SHALL win. `picks`, `mise_extra` and `[mcp].servers` SHALL be unioned.

#### Scenario: Team plus personal
- **WHEN** `devbox-setup --profile team.toml --profile me.toml --yes` runs, `team.toml` has picks and MCP servers, and `me.toml` has `[git]` and `user = "ada"`
- **THEN** the team's items are installed for user `ada` with ada's git identity

### Requirement: Named profile layers replace
A profile MAY set `name`. Answers SHALL record which picks each named profile contributed. Applying a profile with the same `name` again SHALL replace that layer's picks. The review screen, or the output under `--yes`, SHALL list the dropped items. Dropped items SHALL NOT be uninstalled. Picks made by hand SHALL NOT be dropped by any profile.

#### Scenario: Team drops an item
- **WHEN** the VM applied `name = "team"` with `docker` and `k8s`, and later applies a `team` profile with only `docker`
- **THEN** `k8s` is no longer in the saved picks, the output lists it as dropped, and kubectl is still installed

#### Scenario: Hand pick kept
- **WHEN** the user had also picked `lazygit` by hand
- **THEN** `lazygit` stays picked after the team profile changes

### Requirement: Target user
A profile's `user` key, or `--user`, SHALL choose the account devbox-setup configures. If the account does not exist, it SHALL be created with the same groups as pmox's cloud-init user. `--user` SHALL win over the profile.

#### Scenario: New user
- **WHEN** a profile has `user = "ada"` and the VM has no `ada`
- **THEN** `ada` is created, and the user items are applied in `/home/ada`

### Requirement: Version lock
`devbox-setup export --lock` SHALL add a `[lock]` table with the resolved mise tool versions and the apt package versions for the picked items. A profile with `[lock]` SHALL install those exact mise versions. It SHALL install those apt versions when the archive still has them, and otherwise the current one, with a warning naming the package.

#### Scenario: Rebuild next month
- **WHEN** a profile exported with `--lock` lists `kubectl = "1.31.2"` and is applied to a fresh VM after 1.32 is out
- **THEN** `kubectl version --client` reports 1.31.2

### Requirement: Profiles never carry secrets
A profile SHALL be rejected if it contains a key that a `cred` step declares as secret, naming the key. Credential items MAY be listed in `picks`, so their prompts or skeletons run.

#### Scenario: Token in a profile
- **WHEN** a profile has `[npmrc] token = "…"`
- **THEN** devbox-setup exits non-zero naming `token` as a secret that profiles can't hold
