# Powerlevel10k config written by devbox-setup ("Powerlevel10k zsh prompt"):
# p10k's lean preset, then a one-line style with the OS icon, the time and
# Nerd Font icons. Run `p10k configure` to make your own; it replaces this
# file, and devbox-setup never overwrites an existing ~/.p10k.zsh.

source ~/.oh-my-zsh/custom/themes/powerlevel10k/config/p10k-lean.zsh

() {
  emulate -L zsh
  typeset -g POWERLEVEL9K_LEFT_PROMPT_ELEMENTS=(os_icon dir vcs prompt_char)
  # Lean's right side on the same line, plus the time.
  typeset -g POWERLEVEL9K_RIGHT_PROMPT_ELEMENTS=(${POWERLEVEL9K_RIGHT_PROMPT_ELEMENTS:#newline} time)
  typeset -g POWERLEVEL9K_MODE=nerdfont-v3
  typeset -g POWERLEVEL9K_PROMPT_ADD_NEWLINE=false
  typeset -g POWERLEVEL9K_RULER_FOREGROUND=242
  typeset -g POWERLEVEL9K_MULTILINE_FIRST_PROMPT_GAP_FOREGROUND=242
  typeset -g POWERLEVEL9K_VCS_BRANCH_ICON='\uF126 '
  unset POWERLEVEL9K_BATTERY_STAGES # an array in the preset, a string here
  typeset -g POWERLEVEL9K_BATTERY_STAGES='\UF008E\UF007A\UF007B\UF007C\UF007D\UF007E\UF007F\UF0080\UF0081\UF0082\UF0079'
  typeset -g POWERLEVEL9K_TRANSIENT_PROMPT=always
  typeset -g POWERLEVEL9K_INSTANT_PROMPT=quiet
}

# The preset points `p10k configure` at itself; point it back here.
typeset -g POWERLEVEL9K_CONFIG_FILE=${${(%):-%x}:a}

(( ! $+functions[p10k] )) || p10k reload
