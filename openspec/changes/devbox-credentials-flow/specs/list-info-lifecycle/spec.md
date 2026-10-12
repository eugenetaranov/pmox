## ADDED Requirements

### Requirement: Forget devbox credentials on delete
Before stopping a running VM, `pmox delete` SHALL check over SSH whether `/var/lib/devbox-setup/created.json` exists on it. When it does:
- With `--forget`, it SHALL run `sudo -n devbox-setup forget --yes` on the VM.
- With `--no-forget`, it SHALL skip the step.
- With neither flag, interactive use SHALL ask "Remove the credentials devbox-setup created on <vm> first?" (default yes). Non-interactive use and `--yes` SHALL skip it and print a one-line hint naming `--forget`.

A failed or unreachable check or forget SHALL print a warning and SHALL NOT stop the delete. Stopped VMs SHALL be deleted without the step.

#### Scenario: Interactive delete
- **WHEN** `pmox delete web1` runs in a terminal against a running VM with `created.json`, and the user accepts
- **THEN** `devbox-setup forget --yes` runs on web1 before the Shutdown call

#### Scenario: Scripted delete
- **WHEN** `pmox delete --yes web1` runs against the same VM
- **THEN** forget does not run, and stderr contains a hint naming `--forget`

#### Scenario: SSH unreachable
- **WHEN** the SSH check times out
- **THEN** pmox warns and continues with the delete
