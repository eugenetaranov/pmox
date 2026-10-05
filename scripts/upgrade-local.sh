#!/usr/bin/env bash
# Wait for a tag's Release workflow, confirm the GitHub release and Homebrew
# formula are published, then upgrade the local Homebrew install.
#
# Usage: scripts/upgrade-local.sh [TAG]   (default: latest vX.Y.Z tag)
# Brew steps are skipped when Homebrew is not installed.
set -euo pipefail

# gh must never talk to the terminal: on a TTY it pages output through
# $PAGER and probes the terminal (color/cursor queries), which can stall or
# abort the script. Every gh call below has its stdout captured; these
# settings are a second line of defense.
export GH_PAGER=cat PAGER=cat GH_PROMPT_DISABLED=1 NO_COLOR=1

trap 'echo "error: command failed (line $LINENO): $BASH_COMMAND" >&2' ERR

REPO="eugenetaranov/pmox"
TAP_REPO="eugenetaranov/homebrew-tap"
TAP="eugenetaranov/tap"
FORMULA="pmox"

TAG="${1:-}"
if [ -z "$TAG" ]; then
  git fetch --tags -q origin 2>/dev/null || true
  TAG=$(git tag --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
fi
if [ -z "$TAG" ]; then
  echo "error: no release tag found" >&2
  exit 1
fi
VERSION="${TAG#v}"
echo "==> Release $TAG"

command -v gh >/dev/null || { echo "error: gh CLI is required" >&2; exit 1; }

# 1. Release workflow for the tag
RUN_ID=$(gh run list -R "$REPO" --workflow Release --branch "$TAG" -L 1 \
  --json databaseId -q '.[0].databaseId // empty')
if [ -z "$RUN_ID" ]; then
  # The run lookup only serves to wait for an in-flight release; the release
  # and tap checks below still verify the result, so carry on.
  echo "warning: could not find the Release workflow run for $TAG; checking the published release directly" >&2
  if [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ]; then
    echo "         (GH_TOKEN/GITHUB_TOKEN is set; it may lack Actions read access)" >&2
  fi
  STATUS="completed success"
else
  STATUS=$(gh run view -R "$REPO" "$RUN_ID" --json status,conclusion -q '.status + " " + .conclusion')
fi
if [ "${STATUS%% *}" != "completed" ]; then
  echo "==> Waiting for Release workflow (run $RUN_ID)..."
  gh run watch -R "$REPO" "$RUN_ID" --exit-status --interval 20 </dev/null >/dev/null 2>&1 || {
    echo "error: Release workflow failed: $(gh run view -R "$REPO" "$RUN_ID" --json url -q .url)" >&2
    exit 1
  }
elif [ "${STATUS#* }" != "success" ]; then
  echo "error: Release workflow concluded '${STATUS#* }': $(gh run view -R "$REPO" "$RUN_ID" --json url -q .url)" >&2
  exit 1
fi
if [ -n "$RUN_ID" ]; then
  echo "==> Release workflow succeeded"
fi

# 2. GitHub release published
RELEASE_URL=$(gh release view -R "$REPO" "$TAG" --json url -q .url </dev/null) || {
  echo "error: GitHub release $TAG not found" >&2
  exit 1
}
echo "==> Published: $RELEASE_URL"

# 3. Homebrew formula updated in the tap
TAP_VERSION=$(gh api "repos/$TAP_REPO/contents/Formula/$FORMULA.rb" -q .content \
  | base64 -d | sed -n 's/^ *version "\(.*\)"/\1/p' | head -1)
if [ "$TAP_VERSION" != "$VERSION" ]; then
  echo "error: tap formula is at $TAP_VERSION, expected $VERSION" >&2
  exit 1
fi
echo "==> Tap formula at $TAP_VERSION"

# 4. Local upgrade
if ! command -v brew >/dev/null; then
  echo "==> Homebrew not installed; skipping local upgrade"
  exit 0
fi

TAP_DIR=$(brew --repository "$TAP")
if [ -d "$TAP_DIR/.git" ]; then
  git -C "$TAP_DIR" pull --ff-only -q
else
  brew tap "$TAP"
fi

if brew list --formula "$TAP/$FORMULA" >/dev/null 2>&1; then
  HOMEBREW_NO_AUTO_UPDATE=1 brew upgrade "$TAP/$FORMULA" || true
else
  HOMEBREW_NO_AUTO_UPDATE=1 brew install "$TAP/$FORMULA"
fi

INSTALLED=$("$(brew --prefix)/bin/$FORMULA" --version 2>/dev/null | awk '{print $3}')
if [ "$INSTALLED" != "$VERSION" ]; then
  echo "error: installed $FORMULA is $INSTALLED, expected $VERSION" >&2
  exit 1
fi
echo "==> Local $FORMULA is $INSTALLED"
