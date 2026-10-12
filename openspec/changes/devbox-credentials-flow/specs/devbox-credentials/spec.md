## ADDED Requirements

### Requirement: Ask-or-skip for every credential
Every catalog item that needs a credential SHALL ask for each value once, during the prompt step, and SHALL accept an empty answer meaning "later". Secret-looking keys SHALL be read without echo. An empty answer SHALL keep the value already in the file. Typed values SHALL be written straight to the item's config file, owned by the target user, with mode 0600. They SHALL NOT appear in saved answers, the log, argv or the dry-run output.

Whether or not a value was typed, the config file SHALL exist after the run, built from the item's skeleton, with every key present and a comment saying where to get it.

#### Scenario: Value typed
- **WHEN** the user picks `npmrc`, enters scope `@acme` and a token
- **THEN** `~/.npmrc` holds a devbox-setup block mapping `@acme` to its registry and the token, with mode 0600 and owned by the user
- **AND** `answers.json` contains `@acme` but not the token

#### Scenario: Value skipped
- **WHEN** the user picks `agentkeys` and leaves every key empty
- **THEN** `~/.config/devbox/agent.env` exists with mode 0600 and holds `ANTHROPIC_API_KEY=`, `OPENAI_API_KEY=` and `GEMINI_API_KEY=`, each under a comment with its key page

#### Scenario: Re-run keeps values
- **WHEN** devbox-setup runs again and the user presses Enter at a secret prompt whose key is already set
- **THEN** the file keeps the existing value

### Requirement: Secrets to fill in
`~/devbox-setup.txt` SHALL have a "Secrets to fill in" section, rebuilt at the end of every run from the current contents of each picked item's credential files. Each credential whose required value is empty SHALL get one line, giving:
- the file path
- the key name
- where to get the value
- the command that applies it once filled in, if one is needed (for example `mcp-sync`, `gh auth login --with-token`, `docker login <registry>`)

When nothing is missing, the section SHALL say so in one line.

The run's closing output SHALL print how many secrets are still missing and point at the file.

An empty value SHALL NOT be listed when the tool reports it is already logged in another way. For example, `ANTHROPIC_API_KEY` is not listed when Claude Code already has credentials.

#### Scenario: Two missing
- **WHEN** a run leaves `LINEAR_API_KEY` in `~/.mcp/linear/env` and `OPENAI_API_KEY` in `~/.config/devbox/agent.env` empty
- **THEN** `~/devbox-setup.txt` lists both, each with its file, key, key page and apply command (`mcp-sync` for Linear, none for OpenAI)
- **AND** the run ends with "2 secrets to fill in — see ~/devbox-setup.txt"

#### Scenario: Filled in by hand later
- **WHEN** the user fills in `OPENAI_API_KEY` by hand and runs `sudo devbox-setup --yes`
- **THEN** the OpenAI line is no longer in the section

### Requirement: --yes never waits for input
With `--yes`, or when stdin is not a terminal, devbox-setup SHALL NOT prompt and SHALL NOT start any step that waits for user input. That includes the GitHub device login, `gh auth refresh` and `tailscale up` without a key. Each step it skips this way SHALL leave a note naming the command to run instead. Credential files SHALL still be written from their skeletons.

#### Scenario: Unattended re-apply on a fresh VM
- **WHEN** a launch hook runs `sudo devbox-setup --yes` with saved answers that include `github` and `mcpjungle`, and no terminal
- **THEN** the run finishes without waiting
- **AND** the notes say GitHub is not logged in and name `gh auth login`
- **AND** each MCP server's env file exists, and its empty keys appear under "Secrets to fill in"

### Requirement: Agent API keys
An `agentkeys` item on the AI tab SHALL manage `~/.config/devbox/agent.env` (0600). The file SHALL hold `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and `GEMINI_API_KEY`, each asked only when the matching agent CLI is picked or installed. `shell.sh` SHALL export the file's values in interactive shells only. devbox-setup SHALL NOT write the keys to `/etc/environment`, systemd units or any file another user can read.

#### Scenario: Only Codex installed
- **WHEN** `codex` is picked and `claude` and `gemini` are not
- **THEN** only `OPENAI_API_KEY` is asked for

#### Scenario: Not inherited by services
- **WHEN** `agent.env` holds `OPENAI_API_KEY`
- **THEN** a systemd service started by devbox-setup, such as mcpjungle, does not have the key in its environment

### Requirement: Package and registry credentials
- An `npmrc` item SHALL ask for an npm scope, a registry URL (default `https://npm.pkg.github.com`) and a token. It SHALL keep its lines in a marked block in `~/.npmrc`, replacing only that block.
- A `registries` item SHALL ask for one or more container registries. It SHALL log in to each with `docker login --password-stdin` as the user.
- For `ghcr.io`, when gh is logged in with the `read:packages` scope, the password SHALL default to `gh auth token`.
- A registry left without a password SHALL be listed under "Secrets to fill in", with its `docker login` command.

#### Scenario: ghcr through gh
- **WHEN** gh is logged in with `read:packages` and the user adds `ghcr.io` with an empty password
- **THEN** devbox-setup logs in to ghcr.io with the gh token and lists nothing for it

#### Scenario: Existing .npmrc kept
- **WHEN** `~/.npmrc` already has lines outside the devbox-setup block
- **THEN** those lines are unchanged after the run

### Requirement: age key for sops
An `agekey` item, needing `sops`, SHALL offer three choices:
- **generate** a new key with `age-keygen`
- **paste** an existing one
- **later**: a skeleton file and a note

The key SHALL live at `~/.config/sops/age/keys.txt` with mode 0600, and `shell.sh` SHALL set `SOPS_AGE_KEY_FILE` to it. When a key is generated, the public key SHALL be printed and written to `~/devbox-setup.txt`.

#### Scenario: Generate
- **WHEN** the user picks generate
- **THEN** the key file exists at 0600 and the run prints its `age1…` public key

#### Scenario: Existing key kept
- **WHEN** `~/.config/sops/age/keys.txt` already holds a key
- **THEN** devbox-setup does not offer generate and does not change the file

### Requirement: Record of what devbox-setup created
devbox-setup SHALL record what it creates in `/var/lib/devbox-setup/created.json` (0600, root), writing each entry as it is created:
- uploaded GitHub keys (id, title, kind auth or signing)
- whether it logged gh in
- registries it logged in to
- credential files it created, or managed blocks it added to existing files
- whether it logged Tailscale in

It SHALL NOT record secret values.

#### Scenario: Key upload recorded
- **WHEN** devbox-setup uploads `~/.ssh/id_ed25519.pub` to GitHub
- **THEN** `created.json` holds that key's id and title, and nothing secret

### Requirement: devbox-setup forget
`devbox-setup forget` SHALL list everything in `created.json` and ask for confirmation, unless `--yes` is given. It SHALL then, in this order:
1. delete the recorded GitHub keys while gh is still logged in
2. log out of the recorded registries
3. empty every value in the recorded credential files and remove recorded managed blocks, leaving the skeletons
4. log gh out, if devbox-setup logged it in
5. log Tailscale out, if devbox-setup logged it in

It SHALL then print the pages where tokens it cannot revoke itself are revoked at the source. It SHALL NOT delete files, keys or logins it did not record. It SHALL continue past a failed step, report it, and exit non-zero.

#### Scenario: Forget after a normal setup
- **WHEN** devbox-setup had uploaded a GitHub key, logged gh in and created `agent.env`, and the user runs `sudo devbox-setup forget --yes`
- **THEN** the key is gone from the GitHub account, gh is logged out and `agent.env` holds only empty values
- **AND** the output lists the Anthropic and OpenAI key pages for revoking at the source

#### Scenario: Key uploaded before the record existed
- **WHEN** `created.json` has no GitHub entries
- **THEN** forget deletes no GitHub key, and prints the github.com/settings/keys link for checking by hand

#### Scenario: User's own files untouched
- **WHEN** `~/.npmrc` existed before devbox-setup added its block
- **THEN** forget removes only the block and keeps the file
