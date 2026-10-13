#!/bin/sh
# End-to-end check of the Go devbox-setup on a throwaway VM: launch it with
# a profile (every catalog item, or ITEMS="cli tmux …"; MCP="github …"
# picks MCP servers and wires agents; TZ_SET=Europe/Berlin sets the
# timezone), verify, re-run, delete. Needs a
# configured pmox server. KEEP=1 keeps the VM.
set -eu
PMOX=${PMOX:-bin/pmox}
NAME=${NAME:-devbox-e2e-$(date +%s)}
ITEMS=${ITEMS:-$(go run ./cmd/devbox-setup --list | cut -f1 | tr '\n' ' ')}
profile=$(mktemp "${TMPDIR:-/tmp}/devbox-e2e.XXXXXX")
cleanup() {
  rm -f "$profile"
  if [ "${KEEP:-0}" != 1 ]; then "$PMOX" delete "$NAME" --yes --no-forget </dev/null || true; fi
}
trap cleanup EXIT

{
  printf 'picks = ['
  sep=
  for id in $ITEMS; do printf '%s"%s"' "$sep" "$id"; sep=', '; done
  printf ']\n'
  if [ -n "${TZ_SET:-}" ]; then printf '[system]\ntz = "%s"\n' "$TZ_SET"; fi
  if [ -n "${MCP:-}" ]; then
    printf '[mcp]\nwire_agents = true\nservers = ['
    sep=
    for n in $MCP; do printf '%s"%s"' "$sep" "$n"; sep=', '; done
    printf ']\n'
  fi
} > "$profile"
echo "== $NAME: $(echo $ITEMS | wc -w | tr -d ' ') items"

"$PMOX" launch "$NAME" --devbox-profile "$profile" --strict-hooks \
  --cpu "${CPU:-4}" --mem "${MEM:-8}" --disk "${DISK:-60}" --wait "${WAIT:-10m}" </dev/null
echo "== verify"
"$PMOX" exec "$NAME" -- devbox-setup --verify </dev/null
echo "== re-run"
"$PMOX" exec "$NAME" -- sudo devbox-setup --yes </dev/null
echo "== verify again"
"$PMOX" exec "$NAME" -- devbox-setup --verify </dev/null
echo "== ok: $NAME"
