## Why

devbox-setup's zsh gets a fixed plugin list (`git terraform kubectl aws
wd` plus history search, autosuggestions and syntax highlighting),
whatever was picked. So:
- Most installed tools have no Tab completion: helm, docker, gh, mise
  and opentofu have oh-my-zsh plugins that are never enabled.
- terragrunt, k9s, stern, argocd, flux, kind, k3d, talosctl, packer,
  vault, doctl, hcloud, just, task, uv, ruff, rustup/cargo and others
  have no plugin at all.
- Plugins for tools that aren't installed load anyway.

Completion also has to work in the usual zsh way. The user's own
`~/.zshrc` shows what "nice" means: oh-my-zsh plugins for git, aws,
terraform and kubectl, `aws_completer`, a menu you move through with
the arrow keys, fzf, and a Terraform plugin cache.

## What Changes

- **Plugins follow the installed tools.**
  - The rendered `.zshrc` enables a base set every time: `git`, `wd`,
    `sudo`, `extract` and `colored-man-pages`.
  - It adds each tool plugin only when the tool is on PATH when the
    shell starts: aws, terraform, opentofu, kubectl, kubectx, helm,
    docker, gh, mise, rust, golang.
  - A tool installed later is picked up by the next shell, with no
    rewrite.
- **Completions for tools without a plugin.**
  - A zsh drop-in, `~/.config/devbox/completions.zsh`, caches each
    installed tool's own completion script (`<tool> completion zsh`
    and similar) in `~/.cache/devbox/zsh-completions`.
  - It adds that directory to `fpath` before oh-my-zsh runs
    `compinit`.
  - A cache entry is regenerated when its binary is newer, so upgrades
    stay current.
  - HashiCorp-style tools (terragrunt, vault, packer) are wired with
    `complete -C`.
- **Fuzzy Tab menu, opt-in.** A new item, "zsh fuzzy Tab completion
  (fzf-tab)", needs `shell` and `cli` (fzf). Tab then opens an fzf list
  of candidates, with directory previews when `eza` is installed.
- **Tool niceties in the shell drop-in**, from the user's setup:
  - When terraform or tofu is installed, `TF_PLUGIN_CACHE_DIR` points
    at `~/.terraform.d/plugin-cache` and the directory is created.
  - When `kubecolor` is installed, `kubectl` is aliased to it and keeps
    kubectl's completion.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `devbox-shell-config`:
  - the `zsh plugins` requirement becomes plugins chosen by installed
    tools
  - new requirements: completion cache, fzf-tab item, tool niceties in
    the drop-in

## Impact

- Assets in `internal/bootstrap`:
  - `assets/zshrc.tmpl`: tool-gated plugin list, completions drop-in
    before oh-my-zsh
  - new `conf/completions.zsh`
  - `conf/shell.sh`: TF plugin cache, kubecolor
- `devbox-setup`:
  - the `shell` item installs the completions drop-in and primes the
    cache
  - new `fzftab` catalog row and install step
- `catalog_test.go` and `bootstrap_test.go`; README devbox section.
- Startup cost: one `stat` per cached tool per shell. Completion
  scripts are generated only when missing or stale, never on every
  start.
