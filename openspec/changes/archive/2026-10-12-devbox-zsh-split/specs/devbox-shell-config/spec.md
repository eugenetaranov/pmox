## ADDED Requirements

### Requirement: Separate zsh items
The Shell & CLI tab SHALL offer zsh as separate items, in this order, each of whose labels contains "zsh" so that searching `zsh` lists them all, together with `fzftab` and `shellcfg`:
- `zsh` — "zsh (login shell)": installs zsh and makes it the user's login shell; nothing else.
- `zshcfg` — "zsh config: oh-my-zsh + autosuggestions, syntax highlighting, history search, completions", needing `zsh`: installs oh-my-zsh and its plugins and writes `.zshrc` from the template, keeping an existing one as `.zshrc.orig`. The theme SHALL be powerlevel10k when it is installed and oh-my-zsh's default otherwise, so re-running `zshcfg` keeps the prompt.
- `p10k` — "Powerlevel10k zsh prompt (lean, one line, Nerd Font icons)", needing `zshcfg`: see the next requirement.

The `shellcfg` label SHALL read "Shell niceties for zsh and bash: z, fzf keys, direnv, eza/bat aliases".

Saved answers containing the former `shell` item SHALL be read as `zsh zshcfg p10k`.

#### Scenario: zsh only
- **WHEN** only `zsh` is picked
- **THEN** the user's login shell is zsh and no oh-my-zsh, `.zshrc` template or prompt theme is installed

#### Scenario: Prompt pulls in the rest
- **WHEN** only `p10k` is picked
- **THEN** the plan contains `zsh`, `zshcfg` and `p10k`, in that order

#### Scenario: Searching zsh
- **WHEN** the user types `zsh` in the picker
- **THEN** `zsh`, `zshcfg`, `p10k`, `fzftab` and `shellcfg` are the first five results

#### Scenario: Old answers
- **WHEN** saved answers contain `shell` and devbox-setup runs with `--yes`
- **THEN** it applies `zsh`, `zshcfg` and `p10k`

### Requirement: Powerlevel10k prompt preset
The `p10k` item SHALL install powerlevel10k into oh-my-zsh's `custom/themes` and set `ZSH_THEME` to it in `.zshrc`. When `~/.p10k.zsh` does not exist, it SHALL write one that sources powerlevel10k's own `config/p10k-lean.zsh` and then sets:
- left prompt: `os_icon dir vcs prompt_char` on one line; right prompt: lean's segments without `newline`, plus `time`
- `POWERLEVEL9K_MODE=nerdfont-v3`, `PROMPT_ADD_NEWLINE=false`, ruler and gap colour 242, a branch icon, Nerd Font battery stages
- `TRANSIENT_PROMPT=always`, `INSTANT_PROMPT=quiet`

An existing `~/.p10k.zsh` (for example from `p10k configure`) SHALL be kept. The rendered `.zshrc` SHALL start p10k's instant prompt when its cache file exists, and do nothing otherwise.

#### Scenario: No wizard
- **WHEN** `p10k` is installed and the user opens zsh for the first time
- **THEN** a one-line lean prompt with the OS icon and the time is shown, and the configuration wizard does not start

#### Scenario: Own config kept
- **WHEN** `~/.p10k.zsh` already exists and `p10k` is installed
- **THEN** the file is unchanged

## MODIFIED Requirements

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

#### Scenario: zsh config rewrites zshrc
- **WHEN** `zshcfg` and `shellcfg` are both in the plan
- **THEN** the rendered `.zshrc` still ends with the drop-in source line

#### Scenario: Terraform providers cached per VM
- **WHEN** `terraform` and `shellcfg` are installed and the user runs `terraform init` in two directories using the same provider
- **THEN** the provider is downloaded once, into `~/.terraform.d/plugin-cache`

### Requirement: zsh plugins
The `zshcfg` item SHALL install zsh-autosuggestions, zsh-syntax-highlighting and zsh-fzf-history-search into oh-my-zsh's `custom/plugins`.

The rendered `.zshrc` SHALL build `plugins=()` when the shell starts, before sourcing oh-my-zsh:
- **Always:** `git wd sudo extract colored-man-pages zsh-fzf-history-search`.
- **Each tool plugin, only when its command is on PATH at that moment:**
  - `aws` (aws), `terraform` (terraform), `opentofu` (tofu)
  - `kubectl` (kubectl), `kubectx` (kubectx), `helm` (helm)
  - `docker` (docker), `gh` (gh), `mise` (mise)
  - `rust` (rustup), `golang` (go)
- **Then, in this order:** `fzf-tab` when installed, `zsh-autosuggestions`, and `zsh-syntax-highlighting` last.

A tool installed after `zshcfg` SHALL get its plugin in the next new shell, without re-running devbox-setup.

#### Scenario: Plugins enabled
- **WHEN** `zshcfg` is installed and the user opens zsh
- **THEN** gray history suggestions and command syntax highlighting are active

#### Scenario: AWS completion
- **WHEN** `zshcfg` and `aws` are installed and the user types `aws s3 ` and presses Tab
- **THEN** the s3 subcommands are offered

#### Scenario: Tool plugin follows installation
- **WHEN** `zshcfg` is installed without `k8s`, then `k8s` is installed later, and the user opens a new zsh
- **THEN** `kubectl ` + Tab and `helm ` + Tab complete their subcommands

#### Scenario: No plugin for a missing tool
- **WHEN** `zshcfg` is installed and terraform is not
- **THEN** `terraform` is not in the shell's `plugins` array and the shell starts without errors

### Requirement: Completions for tools without an oh-my-zsh plugin
The `zshcfg` item SHALL install `~/.config/devbox/completions.zsh`, and the rendered `.zshrc` SHALL source it before oh-my-zsh.

**The cache.** For each tool in its table that is on PATH, the drop-in SHALL keep `~/.cache/devbox/zsh-completions/_<tool>`:
- It SHALL generate the file with the tool's own completion command when the file is missing or older than the tool's binary.
- Generation SHALL be bounded by a timeout.
- A generated file SHALL be kept only when it starts with `#compdef`; otherwise the previous file is kept.
- The drop-in SHALL add the directory to `fpath`, so that oh-my-zsh's single `compinit` loads it.
- Whenever it writes a file, it SHALL remove oh-my-zsh's completion dump, so the new completion is seen.

**HashiCorp-style tools.** For `terragrunt`, `vault` and `packer` when present, it SHALL register `complete -o nospace -C <bin> <bin>` through `bashcompinit`, after `compinit`.

**Priming.** devbox-setup SHALL fill the cache when it installs `zshcfg`, and again on every later run that includes `zshcfg`.

#### Scenario: Tool without a plugin completes
- **WHEN** `zshcfg` and `k8s` are installed and the user types `k9s ` and presses Tab
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
- **WHEN** `zshcfg` and `iaclint` are installed and the user types `terragrunt ` and presses Tab
- **THEN** terragrunt's commands are offered

### Requirement: Fuzzy Tab completion item
The catalog SHALL offer `fzftab`:
- tab: Shell & CLI
- label: "zsh fuzzy Tab completion (fzf-tab)"
- needs: `zshcfg` and `cli`

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
- **WHEN** `zshcfg` is installed without `fzftab`
- **THEN** Tab shows zsh's normal completion menu
