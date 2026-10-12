## ADDED Requirements

### Requirement: Corporate proxy
A `proxy` item on the System tab SHALL ask for an HTTP(S) proxy URL and a no-proxy list. It SHALL write:
- `/etc/apt/apt.conf.d/95devbox-proxy`
- the proxy variables in `/etc/environment`
- a Docker systemd drop-in, `docker.service.d/proxy.conf`
- the Docker client `proxies` in the user's `~/.docker/config.json`

devbox-setup SHALL keep the proxy variables when it re-runs itself through sudo, and SHALL pass them to every step it runs as the user.

#### Scenario: Behind a proxy
- **WHEN** `proxy` is set to `http://proxy.acme:3128` and `cli` and `docker` are picked
- **THEN** the apt, mise and Docker image pulls in that run go through the proxy, and a new login shell has `HTTPS_PROXY` set

### Requirement: Internal CA bundle
A `corpca` item on the System tab SHALL take a PEM bundle, either a local path on the VM or an https URL with a SHA-256. It SHALL install the bundle into `/usr/local/share/ca-certificates/` and run `update-ca-certificates`. It SHALL also set `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` and `AWS_CA_BUNDLE` in `shell.sh`, and restart Docker if it is installed.

#### Scenario: Internal endpoint
- **WHEN** the bundle signs `https://git.acme.internal`
- **THEN** `curl`, `node` and `python3 -c 'import requests; requests.get(...)'` reach it without TLS errors

### Requirement: Dotfiles repo
A `dotfiles` item on the Accounts tab SHALL ask for a repo URL, which is saved in answers. It SHALL clone the repo to `~/.dotfiles`, or pull if the clone is already there. It SHALL then run the first of `install.sh`, `bootstrap.sh` or `setup.sh` it finds, or otherwise `stow` every top-level directory. It SHALL run after every other user item, so the files it writes win.

#### Scenario: Install script
- **WHEN** the repo has `install.sh`
- **THEN** devbox-setup runs it as the user, from `~/.dotfiles`, as the last user step

### Requirement: Unattended agent settings
An `agentsafe` item on the AI tab SHALL write safety settings for each installed agent, merging them into existing files rather than replacing them:
- For Claude Code, permission rules in `~/.claude/settings.json` that deny `git push --force`, reads of `~/.mcp/**`, `~/.ssh/**` and `~/.config/devbox/agent.env`, and `sudo`.
- For Codex, `approval_policy` and `sandbox_mode` in `~/.codex/config.toml`.
- A "Pushing" section in the agent notes, asking agents to push only to non-default branches.

#### Scenario: Existing settings kept
- **WHEN** `~/.claude/settings.json` already has an `env` block
- **THEN** after the run it still has that block and also the deny rules

### Requirement: Gateway wired into more agents
Agent wiring SHALL also register the MCPJungle gateway with OpenCode (`mcp` in `~/.config/opencode/opencode.json`) and Goose (`extensions` in `~/.config/goose/config.yaml`), when they are installed. It SHALL do this once, and SHALL keep the other settings in those files.

#### Scenario: OpenCode
- **WHEN** `opencode` and `mcpjungle` are picked with wiring on
- **THEN** `opencode.json` has an `mcpjungle` remote server at the gateway URL

### Requirement: Gateway listen options
The gateway listen choice SHALL offer three options:
- this machine only
- the Tailscale IP only (when Tailscale is up)
- all interfaces with a bearer token

With the token option, devbox-setup SHALL generate the token, store it at 0600 in the gateway's env, and wire agents with it. It SHALL NOT offer all interfaces without auth.

#### Scenario: Tailscale only
- **WHEN** the Tailscale-only option is chosen
- **THEN** the gateway listens on the VM's `100.x` address and not on its LAN address

### Requirement: Smaller additions
The catalog SHALL add:
- `kubelogin` (mise) to the `k8s` item.
- A "Dev server ports" prompt for `ufw`, which opens the listed ports.
- An `autoupdate-tools` item that enables a weekly user timer running `mise up` and `krew upgrade`.
- A closing note when the user was added to the `docker` group, saying that VS Code and Cursor remote servers must be restarted ("Kill VS Code Server on Host") to pick it up.

#### Scenario: Docker group note
- **WHEN** a run adds the user to the `docker` group
- **THEN** the closing output names the remote-editor restart
