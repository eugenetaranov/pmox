## Context

`install_shell` does everything zsh-related. The user's own `~/.p10k.zsh` (89 KB) is p10k's `config/p10k-lean.zsh` plus ~600 bytes of wizard choices. User-data is ~58 KB of PVE's 64 KiB snippet limit.

## Decisions

- **Three items with needs** (`p10k` → `zshcfg` → `zsh`), so picking the prompt alone pulls in the rest via `resolve`, and catalog order installs them in sequence.
- **Theme follows the clone.** `zshcfg` renders `ZSH_THEME` as powerlevel10k when the p10k clone exists, else `robbyrussell`; `p10k` also rewrites the line in an existing `.zshrc`. Either order of runs ends correct.
- **Ship overrides, not the file.** `conf/p10k-devbox.zsh` sources the lean preset from the clone, then sets the differing values and calls `p10k reload`. The right prompt reuses lean's list (`${…:#newline}` + `time`) so p10k's segment list isn't copied.
- **Never overwrite** an existing `~/.p10k.zsh`.
- **Instant prompt** block at the top of the template, guarded on the cache file.
- **Migration:** `shell` → `zsh zshcfg p10k`. The old mapping (`shell vimcfg`) re-added vimcfg on every load; dropped.

## Risks / Trade-offs

- [Nerd Font icons need a Nerd Font in the user's terminal] → label says so; `p10k configure` restyles.
- [Lean preset changes upstream] → overrides only touch a few variables; the preset is what the clone ships.
