## ADDED Requirements

### Requirement: Shell drop-in activates installed tools
A `shellcfg` item, on the Shell & CLI tab, SHALL install `~/.config/devbox/shell.sh`. The drop-in SHALL, for both zsh and bash and only for tools that are present on PATH:
- initialise zoxide
- hook direnv
- load fzf key bindings and completion from `/usr/share/doc/fzf/examples/`
- define the aliases `ls`/`ll`/`la` → eza and `cat` → `bat --paging=never --style=plain`
- set `EDITOR`/`VISUAL` to `nvim` if it is present, else `vim`

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

### Requirement: zsh plugins
The `shell` item SHALL also install zsh-autosuggestions and zsh-syntax-highlighting into oh-my-zsh's `custom/plugins` and enable them, with zsh-syntax-highlighting last in `plugins=()`.

#### Scenario: Plugins enabled
- **WHEN** `shell` is installed and the user opens zsh
- **THEN** gray history suggestions and command syntax highlighting are active

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
