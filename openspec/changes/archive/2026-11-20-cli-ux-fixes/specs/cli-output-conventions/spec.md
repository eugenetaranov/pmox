# Spec Delta

## Purpose

Output rules shared by every pmox command: how the output format is chosen, when colour is used, how errors and optional-value flags read, and how a context is named, so that help text and behaviour agree everywhere.

## ADDED Requirements

### Requirement: Output format shorthand
The persistent `--output` flag SHALL have the shorthand `-o`. `-o json` and `--output json` SHALL be equivalent on every command.

#### Scenario: Shorthand selects JSON
- **WHEN** a user runs `pmox list -o json`
- **THEN** stdout holds the same JSON as `pmox list --output json`

#### Scenario: Shorthand implies no prompts
- **WHEN** a user runs `pmox vm info -o json` with several pmox VMs and no name
- **THEN** pmox exits with the missing-argument error and draws no picker

### Requirement: Colour can be turned off everywhere
With `--no-color`, or with the `NO_COLOR` environment variable set to a non-empty value, pmox SHALL emit no colour or text-style escape sequences from any command, including pickers, the command palette, spinners, wizards and reports. Layout and wording SHALL stay the same.

#### Scenario: Flag reaches a picker
- **WHEN** a user runs `pmox --no-color shell` on a terminal with several pmox VMs
- **THEN** the picker is drawn without colour escape sequences

#### Scenario: Environment variable
- **WHEN** `NO_COLOR=1 pmox doctor` runs on a terminal
- **THEN** the report contains no colour escape sequences

### Requirement: One prefix on the error line
When a command fails, pmox SHALL print one line of the form `Error: <message>`. The message SHALL NOT repeat an error category such as `user input error:`; the category is carried by the exit code only.

#### Scenario: Missing launch name without a terminal
- **WHEN** `pmox launch` runs with stdin not a terminal
- **THEN** stderr shows `Error: missing VM name — usage: pmox launch <name> (example: pmox launch web1)`
- **AND** the exit code is the user-input code

### Requirement: Optional-value flags read cleanly in help
A flag whose value may be omitted SHALL appear in `--help` as `--flag [=value]` followed by its description. Help SHALL NOT show the placeholder pmox uses internally for "no value given".

#### Scenario: launch --tack
- **WHEN** a user runs `pmox launch --help`
- **THEN** the `--tack` line does not contain `pmox-default-playbook`
- **AND** it says that omitting the value uses the default playbook

#### Scenario: apply --default
- **WHEN** a user runs `pmox apply --help`
- **THEN** the `--default` line does not contain `pmox-pick-default`

### Requirement: A context has one name everywhere
Every command that names a context SHALL use the name `pmox context list` shows for it. A context saved without a name SHALL be shown by its host. `pmox context list` SHALL mark the context commands will use, including when it is the only one configured.

#### Scenario: Host info names the context
- **WHEN** one context is configured without a name for `https://192.168.0.185:8006` and a user runs `pmox info`
- **THEN** the `Context:` line shows `192.168.0.185`, not `(unnamed)`

#### Scenario: Only context is marked
- **WHEN** one context is configured and a user runs `pmox context list`
- **THEN** its row carries the current marker
