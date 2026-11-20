# Spec Delta

## ADDED Requirements

### Requirement: One confirmation style
Every yes/no question a command asks before changing or removing something SHALL be one typed line ending in `[y/N]: ` or `[Y/n]: ` on stderr. An empty answer SHALL take the default shown in capitals. No command outside the full-screen wizards (`init`, `context add`, `context edit`, `access setup`) SHALL use a button-style Yes/No picker for such a question. Commands that could already be run without a question SHALL NOT start asking one.

#### Scenario: Resize asks in a line
- **WHEN** a user runs `pmox resize web1 --mem 8` on a running VM on a terminal
- **THEN** the restart question is a single `[y/N]: ` line

#### Scenario: Context picked for deletion
- **WHEN** a user picks a context to delete from the palette
- **THEN** the question is a single `[y/N]: ` line

#### Scenario: Named context is not questioned
- **WHEN** a script runs `pmox context delete lab`
- **THEN** the context is deleted without a question, as before

#### Scenario: Missing destination directory
- **WHEN** `pmox cp ./app web1:~/new/` runs on a terminal and `~/new` does not exist
- **THEN** pmox asks `Create ~/new on web1? [Y/n]: ` and Enter creates it
