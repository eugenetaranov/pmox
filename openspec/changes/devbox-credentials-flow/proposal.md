## Why

Five developer walkthroughs of devbox-setup (full-stack, AI-agent heavy,
platform/SRE, team lead, security-minded) stalled at the same place: the
tools install, but nothing that needs a credential is finished. Agent CLIs
are never logged in. There is no `.npmrc` or registry login. With `--yes`,
the GitHub step waits for a device code. The GitHub step also uploads a key
with no passphrase that reaches the whole account, and nothing removes it
later.

devbox-setup's model stays as it is. It is not tack, and it never stores or
ships credentials. You run it, and paste a key when an item asks. If you
skip, it writes the config file ready to fill in and tells you where it is.
MCP servers already work this way. This change makes every
credential-bearing item work the same way, and makes the GitHub path safe.

## What Changes

- **One flow for every credential.** An item that needs a secret asks for
  it once; nothing is saved in answers. The value is written straight to its
  config file, at mode 0600. Leaving it empty, or running with `--yes`,
  writes the file with empty values. Either way, the item gets a line in a
  new **"Secrets to fill in"** section of `~/devbox-setup.txt`. The line
  gives the file, the key, where to get the value and the command that
  applies it.
- **`--yes` never waits for input.** Steps that would prompt, such as the
  GitHub device login, are skipped and leave a note.
- **New credential items:**
  - agent API keys (Anthropic, OpenAI, Gemini) in one env file
  - `~/.npmrc` for a private npm scope
  - container registry logins (ghcr.io and others)
  - an age key for sops
- **More MCP servers:** GitHub, Linear, Sentry, Postgres, Playwright and
  filesystem. They ship as pmox-owned specs next to the synced tack-roles
  catalog.
- **GitHub access modes.** The `github` item asks how the VM reaches GitHub:
  - forwarded agent: no key on the VM
  - a key on this VM, optionally with a passphrase (today's behaviour)
  - a fine-grained token

  It also prints the SSO-authorize hint after a key upload, picks the git
  email from your verified addresses, and offers an optional item to sign
  commits with SSH.
- **`devbox-setup forget`.** It removes what devbox-setup created and
  recorded: the GitHub key it uploaded, the gh login, the files it wrote
  for credentials, and the Tailscale login. It never touches anything it
  didn't create.
- **pmox: agent and port forwarding.**
  - `pmox shell` and `pmox exec` gain `-A/--forward-agent` and
    `-L/--forward` (repeatable).
  - The server config gains `forward_agent:`.
  - `pmox vm ssh-config` emits `ForwardAgent` when it applies.

  Nothing is stored; this only passes your laptop's agent and ports
  through.

## Capabilities

### New Capabilities
- `devbox-credentials`: the ask-or-skip flow for credentials, the "Secrets
  to fill in" note, `--yes` never waiting for input, the credential items
  and `devbox-setup forget`.
- `devbox-github-access`: GitHub access modes, the SSO hint, verified-email
  choice and SSH commit signing.
- `ssh-forwarding`: `-A`/`-L` on `pmox shell`/`exec`, `forward_agent:` in
  config, and `ForwardAgent` in `pmox vm ssh-config`.

### Modified Capabilities
- `devbox-catalog`: new catalog items (agent keys, npm registry, registry
  logins, age key, commit signing) and pmox-owned MCP server specs.
- `list-info-lifecycle`: `pmox delete` offers to run `devbox-setup forget`
  on the VM first (`--forget` / `--no-forget`).

## Impact

- `internal/bootstrap/devbox-setup`:
  - prompts, the new items, `github_login`, `write_manage_notes`
  - a `forget` subcommand
  - a record of what was created, in `/var/lib/devbox-setup/created.json`
- `internal/bootstrap/picker.py`: the answer fields for the new items (no
  secrets).
- `internal/bootstrap/conf/mcp-catalog/*.spec` (new and pmox-owned) and
  `bootstrap.Files()`. `bootstrap:sync` is unchanged; it still replaces only
  `assets/mcp-catalog`.
- `cmd/pmox/ssh.go`, `cmd/pmox/sshconfig.go`, `internal/config/config.go`.
- README (devbox-setup credentials, ssh forwarding) and llms.txt.
- The Phase 2 Go rewrite (`devbox-go-rewrite`) carries all of this forward;
  its specs are updated alongside.
