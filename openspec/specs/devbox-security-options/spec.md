## Purpose

Opt-in hardening in devbox-setup (sshd, ufw, fail2ban, needrestart,
automatic security updates) and how it interacts with pmox's boot-time
switch-off of unattended upgrades.
## Requirements
### Requirement: Boot-time auto-upgrade switch-off yields to opt-in
The bootcmd that pmox injects to turn off the apt timers SHALL run only when `/etc/devbox-setup/auto-upgrades` does not exist. The starter cloud-init template SHALL contain the same guarded command.

#### Scenario: Default VM
- **WHEN** a VM launched by pmox boots without the marker
- **THEN** `apt-daily.timer` and `apt-daily-upgrade.timer` are disabled

#### Scenario: Opted in
- **WHEN** a VM has `/etc/devbox-setup/auto-upgrades` and reboots
- **THEN** the apt timers remain enabled

#### Scenario: Unit test
- **WHEN** `bootstrap` injects into an empty cloud-init document
- **THEN** the single bootcmd entry starts with `[ -e /etc/devbox-setup/auto-upgrades ] ||`

### Requirement: Automatic security updates
An `autoupdates` item SHALL:
- install unattended-upgrades;
- write `20auto-upgrades` with both periodic values set to `"1"`;
- write `52devbox` with `Unattended-Upgrade::Automatic-Reboot "false"`;
- create the `/etc/devbox-setup/auto-upgrades` marker;
- enable `apt-daily.timer` and `apt-daily-upgrade.timer`.

When the running VM's cloud-init user-data has the unguarded bootcmd, it SHALL warn that the setting will be undone at the next reboot.

#### Scenario: Survives reboot
- **WHEN** `autoupdates` is installed on a VM launched after this change, and the VM reboots
- **THEN** `systemctl is-enabled apt-daily-upgrade.timer` prints `enabled`

#### Scenario: Old VM warned
- **WHEN** `autoupdates` is installed on a VM whose user-data has the unguarded bootcmd
- **THEN** the run prints a warning that a reboot will turn updates off again

### Requirement: SSH hardening
An `sshharden` item SHALL write `/etc/ssh/sshd_config.d/10-devbox.conf` with:
- `PasswordAuthentication no`
- `KbdInteractiveAuthentication no`
- `PermitRootLogin no`
- `X11Forwarding no`
- `MaxAuthTries 3`
- `ClientAliveInterval 60`
- `ClientAliveCountMax 3`

It SHALL refuse, failing the step with an explanation, when the user's `~/.ssh/authorized_keys` is missing or empty. It SHALL validate with `sshd -t` before reloading ssh, and remove its file if validation fails.

#### Scenario: No keys
- **WHEN** `sshharden` is picked and the user has no authorized keys
- **THEN** the step fails with a message, and sshd's config is unchanged

#### Scenario: Applied
- **WHEN** `sshharden` is installed for a user with an authorized key
- **THEN** `sshd -T` reports `passwordauthentication no` and `permitrootlogin no`

### Requirement: Firewall
A `ufw` item SHALL set default deny incoming and default allow outgoing, and `limit OpenSSH`. It SHALL add these rules when the matching items are in the plan:
- `allow in on tailscale0` for tailscale
- `60000:61000/udp` for mosh
- the mcpjungle port for mcpjungle, when it listens on all interfaces

It SHALL enable ufw last. Its label SHALL warn that Docker's published ports bypass ufw.

#### Scenario: SSH stays reachable
- **WHEN** `ufw` is installed over an SSH session
- **THEN** the session stays connected and new SSH connections succeed

### Requirement: fail2ban
A `fail2ban` item SHALL install `fail2ban` and `python3-systemd`, and enable the sshd jail with the systemd backend, `maxretry=5` and `bantime=1h`. `ignoreip` SHALL include loopback and `100.64.0.0/10`. Its label SHALL say it is for VMs exposed to the internet.

#### Scenario: Jail active
- **WHEN** `fail2ban` is installed
- **THEN** `fail2ban-client status sshd` reports the jail as active

### Requirement: Quiet needrestart
A `needrestart` item SHALL configure needrestart to restart services automatically and to hide kernel and microcode hints for the user's own apt runs.

#### Scenario: No dialog
- **WHEN** the user runs `sudo apt upgrade` and a library update affects services
- **THEN** no whiptail dialog appears, and the affected services are restarted
