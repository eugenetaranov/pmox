#!/bin/sh
# Run by pmox after unpacking devbox-setup's files: installs the Go
# devbox-setup for this machine's architecture (pmox ships both builds),
# or the bash one when there's no build for it. Without bin/ (a pmox built
# without the Go builds) the bash script is already in place.
set -e
share=/usr/local/share/devbox-setup

# sync_team SRC DST EXT: the team's files pmox shipped (from
# ~/.config/pmox/devbox/ on the pushing machine) into DST. The ones pmox
# copied before (listed in DST/.pmox-files) but no longer ships go; other
# files in DST are left alone.
sync_team() {
  src=$1 dst=$2 ext=$3 man=$2/.pmox-files
  [ -d "$src" ] || [ -f "$man" ] || return 0
  mkdir -p "$dst"
  if [ -f "$man" ]; then
    while IFS= read -r f; do
      case "$f" in ''|.*|*/*) continue ;; esac
      [ -f "$src/$f" ] || rm -f "$dst/$f"
    done < "$man"
  fi
  : > "$man.new"
  for p in "$src"/*"$ext"; do
    [ -f "$p" ] || continue
    f=${p##*/}
    install -m 0644 "$p" "$dst/$f"
    echo "$f" >> "$man.new"
  done
  mv -f "$man.new" "$man"
}
sync_team "$share/team/catalog.d" /etc/devbox-setup/catalog.d .toml
sync_team "$share/team/mcp-catalog" /usr/local/share/mcp-catalog .spec

[ -d "$share/bin" ] || exit 0
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) arch= ;;
esac
dst=/usr/local/bin/devbox-setup
if [ -n "$arch" ] && [ -f "$share/bin/devbox-setup-linux-$arch.gz" ]; then
  gunzip -c "$share/bin/devbox-setup-linux-$arch.gz" > "$dst.new"
else
  echo "devbox-setup: no build for $(uname -m); installing the bash version" >&2
  cp "$share/devbox-setup.sh" "$dst.new"
fi
chmod 0755 "$dst.new"
mv -f "$dst.new" "$dst"
rm -rf "$share/bin"
