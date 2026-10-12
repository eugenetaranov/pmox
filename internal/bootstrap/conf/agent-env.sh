# Written by devbox-setup ("Agent API keys"): exports the keys filled in in
# ~/.config/devbox/agent.env. Sourced by interactive shells only, so
# services and cron jobs don't get them.
if [ -r "$HOME/.config/devbox/agent.env" ]; then
  while IFS='=' read -r _devbox_k _devbox_v; do
    case "$_devbox_k" in '' | \#* | *[!A-Za-z0-9_]*) continue ;; esac
    [ -n "$_devbox_v" ] && export "$_devbox_k=$_devbox_v"
  done < "$HOME/.config/devbox/agent.env"
  unset _devbox_k _devbox_v
fi
