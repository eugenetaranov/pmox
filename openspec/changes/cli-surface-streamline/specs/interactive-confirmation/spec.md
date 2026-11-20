# Spec Delta

## ADDED Requirements

### Requirement: One confirmation style
Every yes/no question a command asks before changing or removing something SHALL be asked through the `Confirmer`: one line ending in `[y/N]: ` or `[Y/n]: ` on stderr, answered by typing. No command outside the full-screen wizards SHALL use a button-style Yes/No picker for such a question. `-y`/`--yes` and `PMOX_ASSUME_YES` SHALL skip the question on every command that asks one.

#### Scenario: Resize asks in a line
- **WHEN** a user runs `pmox resize web1 --mem 8` on a running VM on a terminal
- **THEN** the restart question is a single `[y/N]: ` line

#### Scenario: Context delete
- **WHEN** a user runs `pmox context delete lab` on a terminal
- **THEN** the question is a single `[y/N]: ` line, and `--yes` skips it

#### Scenario: Not a terminal
- **WHEN** a command that asks a yes/no question runs without a terminal and without `--yes`
- **THEN** it refuses with the existing non-TTY error naming `--yes`
