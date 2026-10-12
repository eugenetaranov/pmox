## ADDED Requirements

### Requirement: GitHub access modes
When `github` is picked, devbox-setup SHALL ask how the VM reaches GitHub, and SHALL save the choice as `gh_mode` in answers. The three modes:
- **`agent`**: use the SSH agent forwarded from the laptop.
  - No key is generated or uploaded.
  - gh is logged in through the device flow, with scope `user:email`. It never asks for `admin:public_key`.
- **`key`**: generate `~/.ssh/id_ed25519` if absent, with an optional passphrase, and upload it as an auth key.
  - gh scopes are `admin:public_key,user:email`.
- **`token`**: paste a fine-grained personal access token.
  - devbox-setup runs `gh auth login --with-token` and `gh auth setup-git`, and git uses HTTPS.
  - No key is uploaded, and the token is not saved in answers.

The default SHALL be `agent` when an agent with at least one key is reachable during setup, and `key` otherwise.

devbox-setup SHALL keep `SSH_AUTH_SOCK` when it re-runs itself through sudo, so it can make that check.

Saved answers with `github` picked and no `gh_mode` SHALL be treated as `key`.

#### Scenario: Forwarded agent
- **WHEN** the user connected with `pmox shell -A`, picks `github` and accepts the default
- **THEN** no key file is created and no key is uploaded
- **AND** `ssh -T git@github.com` as the user succeeds
- **AND** `~/devbox-setup.txt` says to connect with `pmox shell -A` (or `forward_agent: true`) for git over SSH

#### Scenario: Token mode
- **WHEN** the user picks token and pastes a fine-grained PAT
- **THEN** `gh auth status` is logged in and `git clone https://github.com/<org>/<private>` works
- **AND** `answers.json` contains `gh_mode: token` and no token

#### Scenario: Old answers
- **WHEN** `sudo devbox-setup --yes` runs with answers saved before this change, with `github` picked
- **THEN** it behaves as `key` mode

### Requirement: SSO hint after a key upload
After uploading a key in `key` mode, devbox-setup SHALL print, and add to `~/devbox-setup.txt`, a note that keys for SAML SSO organizations must be authorized at https://github.com/settings/keys ("Configure SSO").

#### Scenario: Key uploaded
- **WHEN** a key is uploaded
- **THEN** the run output contains the SSO note with the settings link

### Requirement: Git email from verified addresses
When the git email is not set, devbox-setup SHALL read the GitHub account's verified emails and offer them, primary first. The noreply address SHALL be used only when the user picks it or it is the only one. Under `--yes`, the primary verified email SHALL be used.

#### Scenario: Work email chosen
- **WHEN** the account has verified `me@acme.com` (primary) and a noreply address
- **THEN** the choice lists `me@acme.com` first, and choosing it sets `user.email`

### Requirement: GitHub host keys pinned
devbox-setup SHALL add github.com's published host keys from a checked-in file to the user's `known_hosts`. It SHALL NOT use `ssh-keyscan`.

#### Scenario: Fresh VM
- **WHEN** `github` runs on a VM without a github.com entry in `known_hosts`
- **THEN** the entries added equal the checked-in keys, and no network scan is made

### Requirement: SSH commit signing
A `gitsign` item on the Accounts tab, needing `github`, SHALL set:
- `gpg.format=ssh`
- `commit.gpgsign=true` and `tag.gpgsign=true`
- `user.signingkey`, which is the forwarded agent's first key (`key::…`) in agent mode, and `~/.ssh/id_ed25519.pub` in key mode
- `gpg.ssh.allowedSignersFile`, pointing to `~/.config/git/allowed_signers` with a line for the git email

It SHALL upload the signing key with `gh ssh-key add --type signing`. If gh lacks `admin:ssh_signing_key`, it SHALL add the scope with `gh auth refresh`, or leave a note when it cannot prompt. In token mode, it SHALL set the git options and note the settings page for adding the signing key by hand.

#### Scenario: Signed commit
- **WHEN** `gitsign` is applied in key mode and the user commits
- **THEN** `git log --show-signature -1` reports a good signature for the git email

#### Scenario: Unattended
- **WHEN** `gitsign` runs under `--yes` and gh lacks the signing scope
- **THEN** git is configured, no prompt is shown, and a note gives the `gh auth refresh -s admin:ssh_signing_key` command
