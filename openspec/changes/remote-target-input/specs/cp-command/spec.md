## ADDED Requirements

### Requirement: Interactive transfer prompts

When `pmox cp` is invoked with no arguments on a terminal, it SHALL ask for the direction (upload or download). It SHALL then use the fields defined in `remote-target-input`, in direction order:
- upload: the local path field, then the target field
- download: the target field, then the local path field

The local path field SHALL accept files and directories. Without a terminal, no arguments SHALL remain a usage error.

#### Scenario: Interactive upload with one VM
- **WHEN** `pmox cp` is invoked with no arguments on a terminal and web1 is the only pmox VM, and the user picks upload
- **THEN** the local field opens with `.` greyed, then the target field opens as `web1:` without asking for a VM

### Requirement: Missing destination directory

`pmox cp` SHALL accept `--mkdir` and SHALL confirm and create a missing destination directory as defined in `remote-target-input`, before running scp.

#### Scenario: Copy into a missing remote directory
- **WHEN** `pmox cp ./app.tar.gz web1:/opt/new/` is invoked on a terminal and `/opt/new` doesn't exist
- **THEN** pmox asks "Create /opt/new/ on web1?" and, on Yes, creates it and runs scp
