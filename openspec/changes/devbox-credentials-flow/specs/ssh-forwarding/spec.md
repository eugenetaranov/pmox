## ADDED Requirements

### Requirement: Agent forwarding
`pmox shell` and `pmox exec` SHALL accept `-A` / `--forward-agent`, which passes `-A` to ssh. A server config field `forward_agent: true` SHALL turn it on for that server's VMs, and `--forward-agent=false` SHALL turn it off for one call. The flag help SHALL state that root on the VM can use the forwarded agent while connected.

#### Scenario: Flag
- **WHEN** `pmox shell -A web1` runs
- **THEN** the ssh argv contains `-A` before `<user>@<ip>`

#### Scenario: Config default and override
- **WHEN** the server has `forward_agent: true` and `pmox exec --forward-agent=false web1 -- true` runs
- **THEN** the ssh argv does not contain `-A`

### Requirement: Port forwarding
`pmox shell` SHALL accept `-L` / `--forward`, repeatable. A bare port `N` SHALL expand to `N:localhost:N`. Any other value SHALL be passed to ssh as `-L <value>` unchanged.

#### Scenario: Two ports
- **WHEN** `pmox shell -L 3000 -L 8000:127.0.0.1:8000 web1` runs
- **THEN** the ssh argv contains `-L 3000:localhost:3000 -L 8000:127.0.0.1:8000`

### Requirement: ForwardAgent in ssh-config
`pmox vm ssh-config` SHALL write `ForwardAgent yes` in the host block when the server has `forward_agent: true` or the command is given `-A`. Otherwise it SHALL write no ForwardAgent line.

#### Scenario: Config on
- **WHEN** the server has `forward_agent: true`
- **THEN** the printed block contains `  ForwardAgent yes`
