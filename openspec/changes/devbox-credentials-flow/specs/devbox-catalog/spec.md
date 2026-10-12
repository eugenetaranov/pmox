## ADDED Requirements

### Requirement: Credential items
The catalog SHALL offer these items. Each one follows the ask-or-skip contract of the `devbox-credentials` capability.

| id | tab | label | what it manages | needs |
|---|---|---|---|---|
| agentkeys | AI | Agent API keys (Anthropic, OpenAI, Gemini) | `~/.config/devbox/agent.env` | |
| npmrc | Dev tools | Private npm registry (scope + token) | managed block in `~/.npmrc` | node |
| registries | System | Container registry logins (ghcr.io, …) | `docker login` per registry | docker |
| agekey | IaC | age key for sops (generate or paste) | `~/.config/sops/age/keys.txt` | sops |
| gitsign | Accounts | Sign commits with SSH | git signing config + signing key on GitHub | github |

The `github` item's label SHALL become "GitHub login (forwarded agent, key or token)".

#### Scenario: Items present
- **WHEN** the picker opens
- **THEN** each item above is on its tab, with its label

#### Scenario: Needs pulled in
- **WHEN** only `gitsign` is picked
- **THEN** the plan also includes `github` and its needs

### Requirement: pmox-owned MCP server specs
pmox SHALL ship its own MCP server specs in `internal/bootstrap/conf/mcp-catalog/`, installed into the same catalog directory as the tack-roles specs. A spec name SHALL NOT exist in both directories; a test SHALL enforce this. `bootstrap:sync` SHALL NOT change the pmox-owned directory.

The pmox-owned specs SHALL include:
- `github` (docker, `GITHUB_PERSONAL_ACCESS_TOKEN`)
- `postgres` (docker with host networking, `DATABASE_URI`)
- `playwright` (docker, no credentials)
- `linear` (http, bearer `LINEAR_API_KEY`)

Each spec SHALL pin its image tag. Each skeleton SHALL say where to create its credential.

#### Scenario: Offered in the MCP picker
- **WHEN** the user picks `mcpjungle`
- **THEN** the MCP server list includes github, postgres, playwright and linear next to the tack-roles servers

#### Scenario: Name clash caught
- **WHEN** a sync from tack-roles adds `github.spec` to `assets/mcp-catalog`
- **THEN** `go test ./internal/bootstrap/...` fails, naming `github`
