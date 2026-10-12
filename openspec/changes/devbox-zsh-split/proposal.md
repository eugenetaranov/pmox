## Why

One catalog item, `shell` ("zsh + oh-my-zsh + p10k"), installs zsh, oh-my-zsh with plugins, completions and the powerlevel10k prompt together, and leaves the prompt to p10k's first-login wizard. Users want zsh, its config and the prompt as separate choices, all found by searching `zsh`, and a ready prompt instead of the wizard.

## What Changes

- **BREAKING (catalog ids):** `shell` is replaced by `zsh` (the shell), `zshcfg` (oh-my-zsh, plugins, `.zshrc`, completions) and `p10k` (the prompt). Saved answers with `shell` are read as all three.
- `p10k` writes a ready `~/.p10k.zsh` (p10k's lean preset plus a one-line, Nerd Font, transient-prompt style) when none exists; no wizard. `.zshrc` starts the instant prompt when available.
- `fzftab` needs `zshcfg`; `shellcfg`'s label says it's for zsh and bash.

## Capabilities

### New Capabilities

### Modified Capabilities
- `devbox-shell-config`: separate zsh items, the p10k preset; existing zsh requirements refer to `zshcfg`.
- `devbox-catalog`: the `shell` relabel is gone.

## Impact

- `internal/bootstrap/devbox-setup`, `assets/zshrc.tmpl`, new `conf/p10k-devbox.zsh` (~1.5 KB of user-data)
- Tests in `internal/bootstrap`; README
