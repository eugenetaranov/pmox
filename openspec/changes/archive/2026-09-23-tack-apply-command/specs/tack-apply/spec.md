## ADDED Requirements

### Requirement: Apply command runs tack against a VM

`pmox apply [vm]` SHALL resolve a target VM (the argument is optional and
falls back to the interactive picker), auto-start it if stopped and wait
for its IP, then run the resolved tack playbook against it. The command
SHALL target a single VM in this version.

#### Scenario: Apply to a named running VM

- **WHEN** the user runs `pmox apply web1` and web1 is running
- **THEN** pmox resolves web1's IP and invokes tack against it with the resolved playbook

#### Scenario: Apply auto-starts a stopped VM

- **WHEN** the target VM is stopped
- **THEN** pmox starts it and waits for its IP before invoking tack

#### Scenario: No argument shows the picker

- **WHEN** the user runs `pmox apply` with no argument on a terminal
- **THEN** the VM picker is shown and the chosen VM is applied

#### Scenario: tack is not installed

- **WHEN** the `tack` binary is not on PATH
- **THEN** pmox exits with a clear error telling the user to install tack, without attempting the run

### Requirement: Playbook resolution ladder

`pmox apply` SHALL resolve which playbook to run using a first-match
ladder: an explicit `--playbook` path, then a profile argument
(`~/.config/pmox/tack/<profile>.yaml`), then the profile last remembered
for that VM, then the default `~/.config/pmox/tack/playbook.yaml`.

#### Scenario: Explicit playbook wins

- **WHEN** `--playbook ./p.yaml` is given
- **THEN** that file is used regardless of any profile or default

#### Scenario: Named profile

- **WHEN** the user runs `pmox apply web1 web`
- **THEN** `~/.config/pmox/tack/web.yaml` is used

#### Scenario: Falls back to the default playbook

- **WHEN** no `--playbook`, profile arg, or remembered profile applies
- **THEN** `~/.config/pmox/tack/playbook.yaml` is used

#### Scenario: Missing playbook is a friendly error

- **WHEN** the resolved playbook file does not exist
- **THEN** pmox prints guidance to create it or run `pmox apply --init`, not a raw tack error

### Requirement: SSH handoff via tack connection flags

`pmox apply` SHALL convey the VM's SSH user and identity to tack via its
connection flags, invoking `tack run <playbook> -c ssh://<user>@<ip>
--ssh-key <identity>`. When pmox runs with `--ssh-insecure` (or
`PMOX_SSH_INSECURE`), it SHALL pass tack's `--ssh-insecure`; otherwise
tack verifies the host key against `~/.ssh/known_hosts`.

#### Scenario: Connection carries pmox's user, IP, and key

- **WHEN** pmox invokes tack for a VM
- **THEN** tack is called with `-c ssh://<user>@<ip>` and `--ssh-key <identity>` using pmox's resolved values

#### Scenario: Insecure passthrough

- **WHEN** the user runs `pmox apply web1 --ssh-insecure`
- **THEN** tack is invoked with `--ssh-insecure`

### Requirement: Plan and apply passthrough

`pmox apply` SHALL let tack's own plan/apply confirmation flow show
through, adding `--auto-approve` only when the user passes `-y` (or sets
`PMOX_ASSUME_YES`), and mapping `--check` to tack's `--check`.
`--tags`/`--skip-tags` and `--output json` SHALL pass through to tack.

#### Scenario: Interactive confirmation shows through

- **WHEN** `pmox apply web1` runs without `-y`
- **THEN** tack's plan/apply prompt is displayed to the user and controls whether changes are applied

#### Scenario: Assume-yes auto-approves

- **WHEN** `pmox apply web1 -y` runs
- **THEN** tack is invoked with `--auto-approve`

#### Scenario: Check performs a plan only

- **WHEN** `pmox apply web1 --check` runs
- **THEN** tack is invoked with `--check` and no changes are applied

### Requirement: Per-VM profile memory

`pmox apply` SHALL remember the profile used for a VM in a local state
file keyed by server and VMID, and reuse it when a later invocation
supplies no `--playbook` and no profile argument. An explicit
`--playbook` MUST NOT update the remembered profile.

#### Scenario: Profile is remembered and reused

- **WHEN** the user runs `pmox apply web1 web` and later runs `pmox apply web1`
- **THEN** the second run reuses the `web` profile

#### Scenario: Explicit playbook does not overwrite memory

- **WHEN** the user runs `pmox apply web1 --playbook ./p.yaml`
- **THEN** any previously remembered profile for web1 is left unchanged

### Requirement: Init scaffold

`pmox apply --init` SHALL create a starter `~/.config/pmox/tack/`
directory containing a `playbook.yaml` and a `roles/` directory without
overwriting existing files.

#### Scenario: Scaffold on a clean system

- **WHEN** the user runs `pmox apply --init` and no tack config exists
- **THEN** `~/.config/pmox/tack/playbook.yaml` and `~/.config/pmox/tack/roles/` are created

#### Scenario: Scaffold never clobbers

- **WHEN** `~/.config/pmox/tack/playbook.yaml` already exists
- **THEN** `--init` leaves it untouched

### Requirement: Launch --tack uses the fixed unified invocation

The launch/clone `--tack` hook SHALL run `tack run` via the same
invocation as `pmox apply` (synthesized inventory), replacing the former
`tack apply` call, and SHALL default to the config playbook when `--tack`
is given without a path.

#### Scenario: Post-create tack run

- **WHEN** `pmox launch web1 --tack ./p.yaml` completes VM creation and SSH is ready
- **THEN** pmox runs `tack run ./p.yaml -c ssh://<user>@<ip> --ssh-key <identity>` against the new VM

#### Scenario: --tack without a path uses the default playbook

- **WHEN** `pmox launch web1 --tack` is run with no path
- **THEN** the default `~/.config/pmox/tack/playbook.yaml` is used

### Requirement: doctor reports tack readiness

`pmox doctor` SHALL report whether `tack` is available on PATH and
whether a default playbook is resolvable, without executing tack.

#### Scenario: tack present and playbook resolvable

- **WHEN** tack is on PATH and `~/.config/pmox/tack/playbook.yaml` exists
- **THEN** doctor reports the tack check as passing

#### Scenario: tack absent

- **WHEN** tack is not on PATH
- **THEN** doctor reports a warning (apply is optional) rather than a hard failure
