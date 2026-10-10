# devbox-setup zsh completions, sourced from ~/.zshrc before oh-my-zsh.
# Written by devbox-setup ("zsh + oh-my-zsh"); re-running it overwrites
# this file.
#
# Tools with an oh-my-zsh plugin (aws, kubectl, helm, gh, mise, docker,
# terraform…) get their completion from the plugin. For the rest, each
# installed tool's own completion script is cached in
# ~/.cache/devbox/zsh-completions and refreshed only when the tool's
# binary is newer than its cached file, so a normal start runs nothing.

typeset -gA _devbox_comp=(
  k9s           'k9s completion zsh'
  stern         'stern --completion zsh'
  just          'just --completions zsh'
  task          'task --completion zsh'
  uv            'uv generate-shell-completion zsh'
  golangci-lint 'golangci-lint completion zsh'
  yq            'yq shell-completion zsh'
  codex         'codex completion zsh'
  zellij        'zellij setup --generate-completion zsh'
  argocd        'argocd completion zsh'
  flux          'flux completion zsh'
  kind          'kind completion zsh'
  k3d           'k3d completion zsh'
  talosctl      'talosctl completion zsh'
  helmfile      'helmfile completion zsh'
  kustomize     'kustomize completion zsh'
  tilt          'tilt completion zsh'
  skaffold      'skaffold completion zsh'
  doctl         'doctl completion zsh'
  hcloud        'hcloud completion zsh'
  trivy         'trivy completion zsh'
  ruff          'ruff generate-shell-completion zsh'
  deno          'deno completions zsh'
  pnpm          'pnpm completion zsh'
  rclone        'rclone completion zsh -'
  restic        'restic generate --zsh-completion /dev/stdout'
  watchexec     'watchexec --completions zsh'
)

_devbox_comp_dir=${XDG_CACHE_HOME:-$HOME/.cache}/devbox/zsh-completions

# devbox_comp_refresh [-q] — (re)generate every stale cache file. -q: no
# output (shell start); without it, report what was written (devbox-setup).
devbox_comp_refresh() {
  local quiet=0 t bin f tmp wrote=0
  [[ $1 == -q ]] && quiet=1
  [[ -d $_devbox_comp_dir ]] || mkdir -p "$_devbox_comp_dir" || return 0
  for t in ${(k)_devbox_comp}; do
    (( $+commands[$t] )) || continue
    bin=${commands[$t]:A}
    f=$_devbox_comp_dir/_$t
    [[ -s $f && ! $bin -nt $f ]] && continue
    tmp=$f.$$
    if (( $+commands[timeout] )); then
      timeout 5 ${(z)_devbox_comp[$t]} >| $tmp 2>/dev/null
    else
      ${(z)_devbox_comp[$t]} >| $tmp 2>/dev/null
    fi
    if [[ -s $tmp ]] && [[ $(head -c 8 -- $tmp) == '#compdef' ]]; then
      mv -f -- $tmp $f && wrote=1
      (( quiet )) || print -r -- "completion: $t"
    else
      rm -f -- $tmp
    fi
  done
  # A new file is invisible to a cached compinit dump: drop it so
  # oh-my-zsh's compinit rebuilds it.
  if (( wrote )); then
    local -a dumps=( ${ZSH_COMPDUMP:-${ZDOTDIR:-$HOME}/.zcompdump}*(N) )
    (( $#dumps )) && rm -f -- $dumps
  fi
  return 0
}

devbox_comp_refresh -q
fpath=($_devbox_comp_dir $fpath)

# devbox_bash_completers — tools that complete themselves bash-style
# (HashiCorp's complete -C). Call after oh-my-zsh has run compinit.
devbox_bash_completers() {
  local t
  for t in terragrunt vault packer; do
    (( $+commands[$t] )) || continue
    if (( ! $+functions[complete] )); then
      autoload -Uz bashcompinit && bashcompinit
    fi
    complete -o nospace -C "${commands[$t]}" "$t"
  done
}
