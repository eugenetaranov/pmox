# shellcheck shell=bash
# devbox-setup shell drop-in, sourced at the end of ~/.zshrc and ~/.bashrc.
# Written by devbox-setup ("Shell niceties"); re-running it overwrites this
# file, so put your own changes in your rc file after the source line.
# Each hook runs only if its tool is installed.

if [ -n "${ZSH_VERSION:-}" ]; then _devbox_sh=zsh; else _devbox_sh=bash; fi

command -v zoxide >/dev/null 2>&1 && eval "$(zoxide init "$_devbox_sh")"
command -v direnv >/dev/null 2>&1 && eval "$(direnv hook "$_devbox_sh")"

# fzf: Ctrl-R history, Ctrl-T files, Alt-C cd. Ubuntu's fzf (0.44) has no
# `fzf --zsh`, so load the scripts it ships. With oh-my-zsh's
# zsh-fzf-history-search, Ctrl-R stays with that plugin.
if command -v fzf >/dev/null 2>&1; then
  for _f in /usr/share/doc/fzf/examples/key-bindings."$_devbox_sh" \
            /usr/share/doc/fzf/examples/completion."$_devbox_sh"; do
    [ -r "$_f" ] && . "$_f"
  done
  unset _f
  # Give Ctrl-R back to zsh-fzf-history-search when oh-my-zsh loaded it.
  if [ "$_devbox_sh" = zsh ] && (( ${+widgets[fzf_history_search]} )); then
    bindkey '^R' fzf_history_search
  fi
fi

if command -v eza >/dev/null 2>&1; then
  alias ls='eza --group-directories-first'
  alias ll='eza -l --git --group-directories-first'
  alias la='eza -la --git --group-directories-first'
  alias lt='eza --tree --level=2'
fi
command -v bat >/dev/null 2>&1 && alias cat='bat --paging=never --style=plain'

if command -v nvim >/dev/null 2>&1; then
  export EDITOR=nvim VISUAL=nvim
else
  export EDITOR=vim VISUAL=vim
fi

unset _devbox_sh
