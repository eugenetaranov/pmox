## 1. zshrc template and plugins

- [x] 1.1 `assets/zshrc.tmpl`: source `~/.config/devbox/completions.zsh` before oh-my-zsh; build `plugins=()` at shell start (base set, tool plugins gated on `$commands`, then fzf-tab if present, autosuggestions, syntax-highlighting last); call the bash-completer hook after `source $ZSH/oh-my-zsh.sh`; fzf-tab zstyles when installed
- [x] 1.2 `devbox-setup` `shell` step: drop the fixed `ZSH_PLUGINS` substitution, install `completions.zsh`, prime the cache as the user

## 2. Completion cache drop-in

- [x] 2.1 `conf/completions.zsh`: tool → command table; regenerate `_tool` when missing or older than `${commands[tool]:A}`, with `timeout 5`, temp file, `#compdef` check; add to `fpath`; remove `$ZSH_COMPDUMP` after writing; `devbox_bash_completers` for terragrunt, vault, packer
- [x] 2.2 Verify every "to verify on a VM" command from design D2 on a VM with those tools installed; drop any that don't print a `#compdef` script

## 3. fzf-tab item and drop-in niceties

- [x] 3.1 Catalog row `fzftab` (Shell & CLI, needs `shell cli`) and its install step (clone Aloxaf/fzf-tab)
- [x] 3.2 `conf/shell.sh`: `TF_PLUGIN_CACHE_DIR` when terraform or tofu is present (mkdir); `kubecolor` alias + `compdef kubecolor=kubectl` in zsh

## 4. Tests and docs

- [x] 4.1 `catalog_test.go` / `bootstrap_test.go`: the new row, the assets embedded, the template contains the drop-in source before oh-my-zsh and syntax-highlighting last
- [x] 4.2 A `zsh -n` syntax check of `completions.zsh` and the rendered template in tests (skipped when zsh is absent)
- [x] 4.3 README devbox section and llms.txt: plugins follow installed tools, the completion cache, fzf-tab, TF plugin cache (llms.txt has no devbox section; README only)
- [x] 4.4 `go vet`, `task lint`, `go test -race ./...`, `openspec validate devbox-zsh-completions`
- [x] 4.5 Real VM: `shell` + `aws` + `terraform` + `k8s` + `iaclint` + `fzftab`; check Tab for `aws s3 `, `terraform `, `kubectl get po`, `helm `, `k9s `, `terragrunt `, `cd ` (fzf-tab); install another tool afterwards and confirm its completion appears in a new shell; measure zsh startup with a warm cache
  - Done on Ubuntu 26.04: plugins came out as `git wd sudo extract colored-man-pages zsh-fzf-history-search aws terraform kubectl kubectx helm gh mise fzf-tab zsh-autosuggestions zsh-syntax-highlighting` (no docker/golang/rust: not installed); Tab completed `aws s3 ` (9 subcommands), `terraform `, `kubectl `, `helm `, `k9s ` (cache), `terragrunt ` (complete -C), and `flux ` after flux was installed later; `cd /` opened fzf-tab with an eza preview. Found and fixed: oh-my-zsh's more specific `menu select` overrode fzf-tab's `menu no`. Cold first shell after installing 26 tools 1.7s; warm start ~0.11s.
