## 1. pmox forwarding

- [x] 1.1 Add `forward_agent` to the server config, with tests
- [x] 1.2 Add `-A/--forward-agent` to `pmox shell` and `pmox exec`, resolving flag > config; `buildSSHArgs` places it before user@ip; argv tests
- [x] 1.3 Add repeatable `-L/--forward` to `pmox shell`, expanding a bare port to `N:localhost:N`; argv tests
- [x] 1.4 Add `ForwardAgent yes` to the `pmox vm ssh-config` block when the config or `-A` asks for it; render tests
- [x] 1.5 Write the flag help with the root-can-use-your-agent warning

## 2. Credential machinery in devbox-setup

- [x] 2.1 Add `CRED_ROWS` (item, file, mode, key, label, how-to-get, apply-command) plus skeleton writers; MCP keeps its prompt and files and feeds the same note
- [x] 2.2 `ask_creds`: one prompt per row for picked items; no echo for secret keys; Enter keeps the current value; not called under `--yes` or without a terminal
- [x] 2.3 `install_creds`: write each file as the user at its mode from its skeleton, set only typed keys, and keep managed blocks between markers
- [x] 2.4 Add a "Secrets to fill in" section to `write_manage_notes`, rebuilt from file contents, with an "already logged in" check per tool; closing line with the count
- [x] 2.5 Make `--yes` never wait for input: guard `github_login`, `gh auth refresh` and `tailscale up` so they leave notes instead; test with no terminal
- [x] 2.6 Record what devbox-setup creates in `created.json` (0600) at each upload, login and file creation; never record values

## 3. Credential items

- [x] 3.1 `agentkeys`: `agent.env`, asking only for the picked or installed CLIs; `shell.sh` sources it in interactive shells only; confirm the Codex non-interactive login on the current release, or leave Codex to `agent.env`
- [x] 3.2 `npmrc`: scope, registry and token in a managed block
- [x] 3.3 `registries`: `docker login --password-stdin` per registry; ghcr.io defaults to `gh auth token` when the scope allows; record each registry
- [x] 3.4 `agekey`: generate, paste or later; `SOPS_AGE_KEY_FILE` in `shell.sh`; print the public key
- [x] 3.5 Add the catalog rows and labels, and the new answer fields in `picker.py` (no secrets); update the catalog tests

## 4. GitHub

- [x] 4.1 `ask_github` with agent, key and token modes; default detection through a preserved `SSH_AUTH_SOCK` (`sudo --preserve-env`); `gh_mode` in answers, with old answers mapping to `key`
- [x] 4.2 Per-mode login scopes (never `admin:public_key` in agent or token mode); token mode uses `--with-token` and `setup-git`
- [x] 4.3 Optional passphrase in key mode; SSO hint after an upload; record the uploaded key id
- [x] 4.4 Verified-email choice from `gh api user/emails`; under `--yes`, use the primary
- [x] 4.5 Add `conf/github_known_hosts`, replace `ssh-keyscan`, and add a test that compares the file with `api.github.com/meta` (skipped when offline)
- [x] 4.6 `gitsign` item: signing config, `allowed_signers`, signing-key upload with a scope refresh, or a note when unattended

## 5. MCP specs

- [x] 5.1 Create `conf/mcp-catalog/` with github, postgres, playwright and linear specs (pinned images, skeleton comments); install them next to `assets/mcp-catalog`
- [x] 5.2 Add a test that fails on a spec name present in both directories; check that `bootstrap:sync` leaves `conf/` alone
- [ ] 5.3 Check each new server registers with mcp-sync on a test VM; settle the Sentry question (add it, or record why not in the design) — Sentry settled (left out, see design); registration on a VM not run yet (needs the tokens)

## 6. forget

- [x] 6.1 Add `devbox-setup forget [--yes]`: list, confirm, then run the steps in the spec's order; continue past a failure and exit non-zero
- [x] 6.2 Print the source-side revoke links for every credential row that has a value
- [x] 6.3 Add `pmox delete --forget/--no-forget`, with the SSH check for `created.json`, the interactive prompt, the `--yes` hint and the warn-and-continue path; tests with a fake ssh seam

## 7. Docs and verification

- [x] 7.1 README: the ask-or-skip flow, "Secrets to fill in", GitHub modes, `forget`, and the `pmox shell -A/-L` and `forward_agent` settings; update llms.txt
- [x] 7.2 Run go vet, `task lint`, `go test -race ./...` and `openspec validate`
- [ ] 7.3 Live check on a fresh VM:
  - [ ] agent mode clones a private repo over `pmox shell -A` — forwarding checked (`ssh-add -l` reaches the local agent with -A, not without); a clone needs a key in your agent
  - [x] a `--yes` run with no terminal finishes and lists the missing secrets
  - [ ] `forget` removes the uploaded key — forget and `pmox delete --forget` checked on files; the GitHub key upload/delete needs your gh login
