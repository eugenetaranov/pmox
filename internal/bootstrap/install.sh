#!/bin/sh
# Run by pmox after unpacking devbox-setup's files: installs the Go
# devbox-setup for this machine's architecture (pmox ships both builds),
# or the bash one when there's no build for it. Without bin/ (a pmox built
# without the Go builds) the bash script is already in place.
set -e
share=/usr/local/share/devbox-setup
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
