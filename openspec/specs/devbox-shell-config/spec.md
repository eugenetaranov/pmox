## Purpose

Shell and git configuration devbox-setup offers so that the tools it
installs are switched on: the shell drop-in, zsh plugins, git defaults
and tmux config.
## Requirements
### Requirement: Shell drop-in activates installed tools
A `shellcfg` item, on the Shell & CLI tab, SHALL install `~/.config/devbox/shell.sh`. The drop-in SHALL, for both zsh and bash and only for tools that are present on PATH:
- initialise zoxide
- hook direnv
- load fzf key bindings and completion from `/usr/share/doc/fzf/examples/`
- define the aliases `ls`/`ll`/`la` → eza and `cat` → `bat --paging=never --style=plain`
- set `EDITOR`/`VISUAL` to `nvim` if it is present, else `vim`
- when `terraform` or `tofu` is present, export `TF_PLUGIN_CACHE_DIR=$HOME/.terraform.d/plugin-cache` and create that directory
- when `kubecolor` is present, alias `kubectl` to `kubecolor`; in zsh, also `compdef kubecolor=kubectl`

A single guarded `source` line SHALL be added to the end of `.zshrc` and `.bashrc`, at most once each.

#### Scenario: Tools activated
- **WHEN** `cli` and `shellcfg` are picked and the user opens a new zsh
- **THEN** `z`, Ctrl-R fzf history and direnv `.envrc` loading all work

#### Scenario: Tool absent
- **WHEN** `shellcfg` is picked without `cli`
- **THEN** a new shell starts with no errors and none of the missing tools' hooks

#### Scenario: Idempotent
- **WHEN** devbox-setup runs `shellcfg` twice
- **THEN** `.zshrc` and `.bashrc` each contain the source line exactly once

#### Scenario: Shell item rewrites zshrc
- **WHEN** `shell` and `shellcfg` are both in the plan
- **THEN** the rendered `.zshrc` still ends with the drop-in source line

#### Scenario: Terraform providers cached per VM
- **WHEN** `terraform` and `shellcfg` are installed and the user runs `terraform init` in two directories using the same provider
- **THEN** the provider is downloaded once, into `~/.terraform.d/plugin-cache`

### Requirement: zsh plugins
The `shell` item SHALL install zsh-autosuggestions, zsh-syntax-highlighting and zsh-fzf-history-search into oh-my-zsh's `custom/plugins`.

The rendered `.zshrc` SHALL build `plugins=()` when the shell starts, before sourcing oh-my-zsh:
- **Always:** `git wd sudo extract colored-man-pages zsh-fzf-history-search`.
- **Each tool plugin, only when its command is on PATH at that moment:**
  - `aws` (aws), `terraform` (terraform), `opentofu` (tofu)
  - `kubectl` (kubectl), `kubectx` (kubectx), `helm` (helm)
  - `docker` (docker), `gh` (gh), `mise` (mise)
  - `rust` (rustup), `golang` (go)
- **Then, in this order:** `fzf-tab` when installed, `zsh-autosuggestions`, and `zsh-syntax-highlighting` last.

A tool installed after `shell` SHALL get its plugin in the next new shell, without re-running devbox-setup.

#### Scenario: Plugins enabled
- **WHEN** `shell` is installed and the user opens zsh
- **THEN** gray history suggestions and command syntax highlighting are active

#### Scenario: AWS completion
- **WHEN** `shell` and `aws` are installed and the user types `aws s3 ` and presses Tab
- **THEN** the s3 subcommands are offered

#### Scenario: Tool plugin follows installation
- **WHEN** `shell` is installed without `k8s`, then `k8s` is installed later, and the user opens a new zsh
- **THEN** `kubectl ` + Tab and `helm ` + Tab complete their subcommands

#### Scenario: No plugin for a missing tool
- **WHEN** `shell` is installed and terraform is not
- **THEN** `terraform` is not in the shell's `plugins` array and the shell starts without errors

### Requirement: Git defaults
A `gitcfg` item, on the Accounts tab and needing `essentials`, SHALL set these with `git config --global` for the user: `pull.rebase=true`, `rebase.autoStash=true`, `rebase.autoSquash=true`, `rebase.updateRefs=true`, `push.autoSetupRemote=true`, `push.default=current`, `fetch.prune=true`, `rerere.enabled=true`, `merge.conflictStyle=zdiff3`, `diff.algorithm=histogram`, `diff.colorMoved=default`, `branch.sort=-committerdate`, `column.ui=auto`, `commit.verbose=true`, `init.defaultBranch=main`. It SHALL also add the aliases `st`, `co`, `br`, `sw`, `lg` and `amend`.

When `delta` is on PATH, it SHALL set `core.pager=delta`, `interactive.diffFilter=delta --color-only`, `delta.navigate=true` and `delta.line-numbers=true`.

It SHALL write `~/.config/git/ignore` from `conf/gitignore_global` only if that file is absent, and SHALL NOT modify `user.name` or `user.email`.

#### Scenario: First push on a fresh branch
- **WHEN** `gitcfg` is installed and the user pushes a new branch with `git push`
- **THEN** the push sets the upstream, with no "no upstream branch" error

#### Scenario: delta used
- **WHEN** `cli` and `gitcfg` are installed
- **THEN** `git diff` pages through delta

#### Scenario: Existing ignore kept
- **WHEN** `~/.config/git/ignore` already exists
- **THEN** its content is unchanged after `gitcfg`

### Requirement: tmux config
A `tmuxcfg` item, needing `tmux`, SHALL write `~/.tmux.conf` from `conf/tmux.conf`, keeping any existing file as `~/.tmux.conf.orig`. The config SHALL enable the mouse, start window and pane numbering at 1, set history to 50000 lines, open splits with `|` and `-` in the current directory, and use 256 colors.

#### Scenario: Existing tmux.conf
- **WHEN** the user has a `~/.tmux.conf` and picks `tmuxcfg`
- **THEN** the old file is saved as `~/.tmux.conf.orig` and the new one is in place

### Requirement: Completions for tools without an oh-my-zsh plugin
The `shell` item SHALL install `~/.config/devbox/completions.zsh`, and the rendered `.zshrc` SHALL source it before oh-my-zsh.

**The cache.** For each tool in its table that is on PATH, the drop-in SHALL keep `~/.cache/devbox/zsh-completions/_<tool>`:
- It SHALL generate the file with the tool's own completion command when the file is missing or older than the tool's binary.
- Generation SHALL be bounded by a timeout.
- A generated file SHALL be kept only when it starts with `#compdef`; otherwise the previous file is kept.
- The drop-in SHALL add the directory to `fpath`, so that oh-my-zsh's single `compinit` loads it.
- Whenever it writes a file, it SHALL remove oh-my-zsh's completion dump, so the new completion is seen.

**HashiCorp-style tools.** For `terragrunt`, `vault` and `packer` when present, it SHALL register `complete -o nospace -C <bin> <bin>` through `bashcompinit`, after `compinit`.

**Priming.** devbox-setup SHALL fill the cache when it installs `shell`, and again on every later run that includes `shell`.

#### Scenario: Tool without a plugin completes
- **WHEN** `shell` and `k8s` are installed and the user types `k9s ` and presses Tab
- **THEN** k9s's subcommands are offered

#### Scenario: Upgrade refreshes the completion
- **WHEN** a cached tool is upgraded so its binary is newer than its cache file, and the user opens a new zsh
- **THEN** that tool's cache file is regenerated, and no other file is

#### Scenario: Broken completion command
- **WHEN** a tool's completion command fails or prints something other than a `#compdef` script
- **THEN** no cache file is written for it and the shell starts without errors

#### Scenario: Normal start does no work
- **WHEN** the cache is current and the user opens a new zsh
- **THEN** no completion command is run

#### Scenario: terragrunt completes
- **WHEN** `shell` and `iaclint` are installed and the user types `terragrunt ` and presses Tab
- **THEN** terragrunt's commands are offered

### Requirement: Fuzzy Tab completion item
The catalog SHALL offer `fzftab`:
- tab: Shell & CLI
- label: "zsh fuzzy Tab completion (fzf-tab)"
- needs: `shell` and `cli`

It SHALL clone `Aloxaf/fzf-tab` into oh-my-zsh's `custom/plugins`. While it is installed, the rendered `.zshrc` SHALL:
- enable it before `zsh-autosuggestions`
- turn zsh's own completion menu off (`menu no`), including oh-my-zsh's more specific `':completion:*:*:*:*:*' menu select`
- when eza is present, set a directory preview for `cd` completion

#### Scenario: Fuzzy menu
- **WHEN** `fzftab` is installed and the user types `cd ` and presses Tab in a directory with several subdirectories
- **THEN** an fzf list of the subdirectories opens, with a preview of the highlighted one

#### Scenario: Works with fzf's own completion
- **WHEN** `fzftab` and `shellcfg` are both installed (fzf's `completion.zsh` takes over Tab)
- **THEN** Tab still opens fzf-tab's list, and `**` + Tab still triggers fzf's fuzzy path completion

#### Scenario: Not installed
- **WHEN** `shell` is installed without `fzftab`
- **THEN** Tab shows zsh's normal completion menu

