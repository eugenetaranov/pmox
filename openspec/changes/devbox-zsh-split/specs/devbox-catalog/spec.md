## MODIFIED Requirements

### Requirement: Self-explanatory labels
Every label SHALL say what the item is, without the user needing to know the tool's name. Items that download more than about 500 MB or build from source SHALL say "large" or give a time estimate. These relabels apply:
- `cli` → "CLI upgrades (ripgrep, fd, bat, eza, fzf, zoxide, delta, direnv)"
- `task` → "Task (Makefile alternative)"
- `omp` → "omp (oh-my-pi coding agent)"
- `docker` → "Docker Engine + Compose", with the docker-group note kept in the source column
- `essentials` → "Essentials (recommended)"
- `azure` and `gcloud` → "(large)" appended

#### Scenario: Large items marked
- **WHEN** the user views the Cloud tab
- **THEN** the Azure CLI and Google Cloud CLI labels include "(large)"
