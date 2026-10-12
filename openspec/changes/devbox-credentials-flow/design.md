## Context

devbox-setup is interactive. You run it once as root (it re-execs through
`sudo`), pick items, answer prompts, and it installs. Saved answers in
`/var/lib/devbox-setup/answers.json` hold picks and plain settings only,
never secrets. `--yes` re-applies them without prompts.

MCP servers already follow the flow we want everywhere:
- `ask_mcp_keys` asks for each key, and an empty answer means "later".
- `install_mcpjungle` writes `~/.mcp/<n>/env` from the spec's skeleton at
  0600, then fills in what was typed.
- `mcp-sync` skips servers that are still missing credentials.

What's missing is that the skip path leaves nothing to find later. The only
hint is one faint line at the end of the run. Every other credential-bearing
item (gh, agent CLIs, npm, Docker registries, sops) either has no
credential step at all or prompts with no skip path.

There are three constraints. devbox-setup never stores or ships
credentials, and pmox never holds them for devbox-setup. `--yes` must be
safe to run from a launch hook with no terminal. Phase 1's SSH delivery is
the only way devbox-setup reaches a VM.

## Goals / Non-Goals

**Goals:**
- One ask-or-skip contract for every credential, with one place that says
  what is still missing (`~/devbox-setup.txt`).
- A GitHub path that needs no long-lived, account-wide key on the VM.
- Undo for what devbox-setup created (`forget`), reachable from
  `pmox delete`.
- Agent and port forwarding from pmox, which the safe GitHub mode and SSO
  callbacks need.

**Non-Goals:**
- Any secret store, sync or copying of credentials between VMs, by pmox or
  devbox-setup.
- Secrets in profiles (Phase 2 keeps "profiles hold no secrets").
- Revoking tokens at their source (Anthropic, npm, Linear…). `forget` lists
  them with a link; it can only delete what it can reach: the GitHub key and
  local files.
- Proxy, internal CA, dotfiles, version locks, per-user state. Those go to
  `devbox-go-rewrite`.

## Decisions

### D1. A credential is a typed record, not ad-hoc code

Each credential-bearing item declares its credentials in one table,
`CRED_ROWS`, next to `CATALOG_ROWS`:

`id|item|file|mode|key|label|how-to-get|apply-command`

`ask_creds` walks the rows for picked items and prompts. Secret-looking keys
use `gum input --password`; an empty answer keeps the current value, or
leaves the field empty. `install_creds` writes each file at its mode, as
the user, from a skeleton, setting only keys that were typed.
`write_manage_notes` re-reads every file. Any row whose key is still empty
becomes a line under "Secrets to fill in", built from the row itself: file,
key, how to get the value and the command that applies it.

This generalises what MCP does today. MCP keeps its own prompt (its keys
come from each spec's skeleton) but feeds the same "Secrets to fill in"
note, so there is one note format. Items whose keys aren't fixed
(`registries`, `agekey`) have their own short prompt and the same note.

*Alternative:* a prompt function per item. That was rejected because it is
how today's gaps happened; the note can't be generated from scattered code.

### D2. Where each credential lives

| Item | File | How it is applied |
|---|---|---|
| `agentkeys` | `~/.config/devbox/agent.env` (0600): `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` | A small `~/.config/devbox/agent-env.sh`, added to `.bashrc` and `.zshrc`, exports the filled-in keys in interactive shells. Codex reads `OPENAI_API_KEY` from there; devbox-setup doesn't call `codex login`. |
| `npmrc` | `~/.npmrc` (0600), a managed block between `# devbox-setup` markers: scope → registry plus `_authToken` | read by npm and pnpm directly |
| `registries` | `~/.docker/config.json` through `docker login --password-stdin` for each registry typed; ghcr.io defaults to `gh auth token` when gh is logged in with `read:packages` | `docker login` |
| `agekey` | `~/.config/sops/age/keys.txt` (0600): generate one, paste one, or leave empty; `SOPS_AGE_KEY_FILE` set in `shell.sh` | sops reads it |
| MCP servers | `~/.mcp/<n>/env` (0600), unchanged | `mcp-sync` |

The env file is sourced only by interactive shells, not by
`/etc/environment` or systemd, so services and cron don't inherit the keys.
Claude Code's own OAuth login (`claude`, then `/login`) remains the default
for subscription users. The row says so, and an empty
`ANTHROPIC_API_KEY` is not reported as missing when `~/.claude` already has
credentials.

### D3. `--yes` never waits for input

- Under `--yes`, or when stdin is not a terminal, `ask_creds` is not called.
  Files are still written from their skeletons, and the note lists what is
  empty.
- `github_login` checks the same thing. Instead of starting the device flow
  it adds a note: "GitHub: not logged in — run `gh auth login` (or
  `sudo devbox-setup` to be asked)".
- Every other interactive step (`gh auth refresh` for the signing scope,
  `tailscale up` without a key) follows the same rule.

### D4. GitHub access modes

`ask_github` asks once and saves the mode in answers (`A_GH_MODE`). The
three modes are below.

**`agent` (the new default when an agent is forwarded at setup time).**
- No key is generated and none is uploaded.
- The git remote stays SSH and authenticates through the forwarded agent,
  so you connect with `pmox shell -A`.
- gh is logged in with the device flow and scopes `user:email`, plus
  `admin:ssh_signing_key` when `gitsign` is picked. It is never given
  `admin:public_key`.
- devbox-setup re-execs with `sudo --preserve-env=SSH_AUTH_SOCK` so it can
  test `ssh-add -l` and `ssh -T git@github.com`.

**`key`: today's behaviour, made explicit.**
- An optional passphrase for the generated key.
- Scopes `admin:public_key,user:email`.
- After the upload, a fixed hint: "If an org uses SAML SSO, authorize this
  key: https://github.com/settings/keys → Configure SSO". The hint is
  printed every time, because finding the orgs needs `read:org`.

**`token`.**
- You paste a fine-grained PAT; it never goes into answers.
- `gh auth login --with-token`, then `gh auth setup-git`, with HTTPS
  remotes.
- No key on GitHub at all.

In all three modes:
- **Email.** devbox-setup reads `gh api user/emails` and offers the verified
  addresses, primary first. The noreply address is used only when you pick
  it, or when it is the only one.
- **Host key.** Today it comes from `ssh-keyscan` and is trusted on first
  use. Instead, devbox-setup writes GitHub's published host keys from
  `conf/github_known_hosts` (checked in, compared with
  `api.github.com/meta` in a test).

*Alternative:* deploy keys per repo. They are deferred, because they need a
repo list and admin rights on each repo. The `token` mode covers the narrow
scope.

### D5. Commit signing (`gitsign`)

`gitsign` needs `github` and sets:
- `gpg.format=ssh`
- `user.signingkey`: `key::<pubkey>` from `ssh-add -L` in agent mode, or
  `~/.ssh/id_ed25519.pub` in key mode
- `commit.gpgsign=true` and `tag.gpgsign=true`
- `gpg.ssh.allowedSignersFile=~/.config/git/allowed_signers`, with a line
  for your email

It uploads the key with `gh ssh-key add --type signing`. If the scope is
missing, it runs `gh auth refresh -s admin:ssh_signing_key`; under `--yes`
it leaves a note instead (D3). Token mode can't upload a signing key, so it
notes the settings page.

### D6. pmox-owned MCP specs

`bootstrap:sync` replaces `assets/mcp-catalog/` from tack-roles. pmox's own
specs therefore live in `conf/mcp-catalog/`, and both directories install
into `/usr/local/share/mcp-catalog`. A test fails if a name exists in both.
The new specs, all on kinds `mcp-sync` already supports:

| Spec | Kind | Credential |
|---|---|---|
| `github` | docker `ghcr.io/github/github-mcp-server:<pinned>` | `GITHUB_PERSONAL_ACCESS_TOKEN` |
| `postgres` | docker `crystaldba/postgres-mcp:<pinned>`, `DOCKER_ARGS="--network host"` | `DATABASE_URI` |
| `playwright` | docker `mcr.microsoft.com/playwright/mcp:<pinned>` | none |
| `linear` | http `https://mcp.linear.app/mcp`, `AUTH=bearer` | `LINEAR_API_KEY` |

The spec skeleton is the documentation: each key gets a comment saying where
to create it, as `jira.spec` does today.

The **filesystem server is not added**. The agents already have the
filesystem, and a docker MCP would need per-user mounts that a spec can't
express. **Sentry** is left out for now (see Open Questions).

### D7. `forget` and the creation record

`/var/lib/devbox-setup/created.json` (0600) records, as each happens:
- the GitHub key ids and titles devbox-setup uploaded (auth and signing)
- whether devbox-setup logged gh in
- the registries it logged in to
- the credential files it created, or the managed blocks it added
- whether it logged Tailscale in

`devbox-setup forget [--yes]` prints that list, asks for confirmation (not
under `--yes`), and then:
1. deletes the recorded GitHub keys with `gh ssh-key delete <id>`, while gh
   is still logged in;
2. runs `docker logout <reg>` for each recorded registry;
3. empties the values in the credential files and removes the managed
   blocks, leaving the skeletons so that a re-run asks again;
4. runs `gh auth logout` and `tailscale logout`.

Last, it prints the source-side revoke links for tokens it can't revoke
(Anthropic, OpenAI, npm, Linear, PATs).

It never deletes a file it didn't create, and never deletes a key it didn't
upload.

`pmox delete` offers to run it:
- If the VM is running and `/var/lib/devbox-setup/created.json` exists (one
  SSH check), interactive delete asks "Remove the credentials devbox-setup
  created on <vm> first?", default yes.
- `--forget` and `--no-forget` decide without asking.
- With `--yes` and neither flag, it skips the step and prints a one-line
  hint.
- If `forget` fails, the delete still goes ahead, with a warning.

### D8. pmox forwarding

- `pmox shell` gains `-A/--forward-agent` and `-L/--forward` (repeatable).
  `-L 3000` expands to `3000:localhost:3000`; a full `L:host:R` is passed
  as given.
- `pmox exec` gains `-A`.
- The server config gains `forward_agent: true`, which turns on `-A` for
  that server's VMs; `--forward-agent=false` overrides it.
- `pmox vm ssh-config` emits `ForwardAgent yes` when the config or its own
  `-A` says so.

The flag help carries a one-line warning: while you're connected, root on
the VM can use your agent.

## Risks / Trade-offs

- [Interactive shells export agent keys to everything started from them]
  This is the same as `export` in an rc file, and it's the reason for the
  0600 file rather than `/etc/environment`. The note says how to scope
  further (direnv per project).
- [A forwarded agent is usable by root on the VM while connected] Agent mode
  is opt-in per connection (`-A`), and the warning says so. People who want
  per-use confirmation can add the key with `ssh-add -c` on the laptop.
- [Pinned MCP images age] They follow the same pinning rule as
  `jira.spec`. The weekly registry workflow does not touch them; bumping
  them is a manual chore listed in the README.
- [`created.json` is lost if the VM is wiped without `forget`] The GitHub key
  title includes the hostname, and `forget` prints the settings link, so a
  manual clean-up is still possible.
- [GitHub host keys change] This is rare; the test against
  `api.github.com/meta` catches it in CI.

## Migration Plan

- Answers gain `gh_mode` and the new item ids. Old answers with `github`
  picked map to `gh_mode=key`, so `--yes` on an existing VM keeps doing
  what it did.
- Existing `~/.mcp/<n>/env` files are untouched; they simply show up in the
  new note when they're incomplete.
- `created.json` starts empty on existing VMs. `forget` then removes nothing
  on GitHub and says so, pointing at the settings page for keys uploaded
  before this release.

## Open Questions

- ~~Sentry~~ Left out for now: its hosted server (`mcp.sentry.dev`) uses
  OAuth, and there is no official image to pin. Revisit when either changes.
- ~~Codex non-interactive login~~ Settled: Codex uses `OPENAI_API_KEY` from
  `agent.env`; no `codex login` call.
