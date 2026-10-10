# cli-progress-feedback Specification

## Purpose
How pmox shows progress while it waits on the cluster, how a spinner
ends, and how commands report a goal that already holds.

## Requirements
### Requirement: Every slow operation shows progress

Every pmox command SHALL show a spinner with a present-tense label (for
example "Starting testbox…", "Loading VMs…") while it waits on the
Proxmox API, a task, the guest agent, node SSH, the access registry or a
download. A command SHALL NOT sit on a bare cursor while it waits. A
multi-step operation SHALL update the label in place as it moves from
step to step, instead of printing a new spinner line per step.

The spinner SHALL be drawn on stderr only, and only when stderr is a
terminal. It SHALL NOT be drawn with `-v`/`--verbose`, `--debug`, or
when stderr is not a terminal; then the command's output SHALL be
exactly what it prints without the spinner.

#### Scenario: Starting a VM from the palette
- **WHEN** a user picks `start` in the `pmox` palette and the VM takes several seconds to boot
- **THEN** a spinner labelled "Starting <name>…" is shown until the VM is up

#### Scenario: Waiting for the guest agent
- **WHEN** `pmox start` has started the VM and is waiting for its IP
- **THEN** the same spinner line changes to "Waiting for <name> to get an IP…"

#### Scenario: Piped output
- **WHEN** stderr is not a terminal
- **THEN** no spinner control sequences are written

### Requirement: No spinner flash for fast operations

A spinner SHALL be drawn only after its operation has run for a short
delay (about 250 ms). An operation that finishes sooner SHALL leave no
trace of a spinner: nothing is drawn and no line is cleared.

#### Scenario: Fast API call
- **WHEN** an operation finishes in 50 ms
- **THEN** no spinner frame is ever written to the terminal

### Requirement: Spinners never overlap prompts or output

A spinner SHALL be stopped, and its line cleared, before any
interactive prompt (picker, confirmation, form) is shown and before the
command prints its result or an error.

#### Scenario: Picker after loading VMs
- **WHEN** a command loads the VM list and then shows the VM picker
- **THEN** the "Loading VMs…" spinner is gone before the picker draws

### Requirement: The spinner line ends in a result

When an operation succeeds, its spinner line SHALL be replaced by a
past-tense result line starting with "✓" that names the object (for
example "✓ testbox started (vmid 100, ip 192.168.0.112)"). When it
fails, the spinner line SHALL be cleared and only the error shown.

#### Scenario: Successful start
- **WHEN** `pmox start testbox` succeeds on a terminal
- **THEN** "Starting testbox…" is replaced by "✓ testbox started" with its VMID and IP

### Requirement: Already in the desired state is success

When a command's goal already holds, it SHALL report that as success
with exit code 0 and a "✓" line describing the state, not as an error.
This covers at least: starting a running VM, stopping a stopped VM,
deleting a VM that is already gone, unpublishing a key that is not
published, revoking access that was not granted, and unmounting a
mount that is not running.

#### Scenario: Starting a running VM
- **WHEN** a user runs `pmox start testbox` and testbox is already running
- **THEN** pmox prints "✓ testbox is already running" with its VMID and IP and exits 0

#### Scenario: Stopping a stopped VM
- **WHEN** a user runs `pmox stop testbox` and testbox is already stopped
- **THEN** pmox prints "✓ testbox is already stopped" and exits 0

