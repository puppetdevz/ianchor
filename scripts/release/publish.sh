#!/usr/bin/env bash
# Renders the Formula for a released tag and publishes it to the Homebrew tap.
#
# Environment:
#   TAG           tag that triggered the release (required, vMAJOR.MINOR.PATCH)
#   SOURCE_TOKEN  token with read access to the source repository (required)
#   TAP_TOKEN     token that can write to the tap main branch (required)
#   IANCHOR_REPO  owner/name of the source repository
#   IANCHOR_TAP_URL  git URL of the tap repository
set -euo pipefail
set +x

: "${TAG:?Missing tag}" "${SOURCE_TOKEN:?Missing source token}" "${TAP_TOKEN:?Missing HOMEBREW_TAP_TOKEN}"

REPO="${IANCHOR_REPO:-puppetdevz/ianchor}"
if [[ "$REPO" != "puppetdevz/ianchor" ]]; then
  echo "::error::Unexpected source repository" >&2
  exit 1
fi
SOURCE_URL="${IANCHOR_SOURCE_URL:-https://github.com/${REPO}.git}"
TAP_URL="${IANCHOR_TAP_URL:-https://github.com/puppetdevz/homebrew-tap.git}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEMPLATE="$ROOT/Formula/ianchor.rb"
PARSER="$ROOT/scripts/release/metadata.rb"

# Only stable vMAJOR.MINOR.PATCH tags, and the tag must match VERSION.
if [[ ! "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::Only stable vMAJOR.MINOR.PATCH tags are supported" >&2
  exit 1
fi
FILE_VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
if [[ "$FILE_VERSION" != "${TAG#v}" ]]; then
  echo "::error::Tag/version mismatch: tag ${TAG} but VERSION is ${FILE_VERSION}" >&2
  exit 1
fi
VERSION="$FILE_VERSION"

# Bind all release inputs to the tested commit, including annotated tags.
HEAD_COMMIT=$(git -C "$ROOT" rev-parse --verify 'HEAD^{commit}')
TAG_COMMIT=$(git -C "$ROOT" rev-parse --verify "refs/tags/${TAG}^{commit}")
if [[ ! "$HEAD_COMMIT" =~ ^[0-9a-f]{40}$ ]] || [ "$HEAD_COMMIT" != "$TAG_COMMIT" ] || [ "${EXPECTED_COMMIT:-}" != "$HEAD_COMMIT" ]; then
  echo "::error::Release tag, event and checkout commit do not match" >&2
  exit 1
fi
if [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=no)" ]; then
  echo "::error::Tracked source files are modified" >&2
  exit 1
fi
check_source_tag() {
  local refs oid peeled
  refs=$(git ls-remote "$SOURCE_URL" "refs/tags/$TAG" "refs/tags/$TAG^{}") || {
    echo "::error::Cannot verify source tag" >&2; return 1;
  }
  oid=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/$TAG" '$2 == ref {print $1}')
  peeled=$(printf '%s\n' "$refs" | awk -v ref="refs/tags/$TAG^{}" '$2 == ref {print $1}')
  if [ -z "$oid" ] || { [ -n "$peeled" ] && [ "$peeled" != "$HEAD_COMMIT" ]; } || { [ -z "$peeled" ] && [ "$oid" != "$HEAD_COMMIT" ]; }; then
    echo "::error::Remote source tag does not match the tested commit" >&2
    return 1
  fi
}
check_source_tag

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
TAP_PATH="$WORK/tap"
TARBALL_PATH="$WORK/source.tar.gz"
TARBALL_URL="https://github.com/${REPO}/archive/${HEAD_COMMIT}.tar.gz"
CANDIDATE="$WORK/ianchor.rb"

# No credential in remote URL, command log or Git config. The helper contains no secret.
printf '%s\n' '#!/bin/sh' 'case "$1" in *Username*) printf "%s\n" "x-access-token" ;; *) printf "%s\n" "$TAP_TOKEN" ;; esac' > "$WORK/askpass"
chmod 700 "$WORK/askpass"
export GIT_ASKPASS="$WORK/askpass" GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=
git clone --branch main --single-branch "$TAP_URL" "$TAP_PATH"
BASE=$(git -C "$TAP_PATH" rev-parse HEAD)
if [ -L "$TAP_PATH/Formula" ] || [ -L "$TAP_PATH/Formula/ianchor.rb" ]; then
  echo "::error::Formula paths must not be symbolic links" >&2
  exit 1
fi
if { [ -e "$TAP_PATH/Formula" ] && [ ! -d "$TAP_PATH/Formula" ]; } || { [ -e "$TAP_PATH/Formula/ianchor.rb" ] && [ ! -f "$TAP_PATH/Formula/ianchor.rb" ]; }; then
  echo "::error::Invalid Formula path type" >&2
  exit 1
fi

# Parse legacy metadata without evaluating arbitrary Ruby from the tap.
if ! ruby "$ROOT/scripts/release/check-existing.rb" "$TAP_PATH/Formula/ianchor.rb" "$VERSION" "$PARSER"; then
  echo "::error::Existing Formula metadata was rejected; inspect the tap before retrying" >&2
  exit 1
fi
if [ -f "$TAP_PATH/Formula/ianchor.rb" ]; then ruby -c "$TAP_PATH/Formula/ianchor.rb" >/dev/null; fi

curl --fail --location --retry 3 --output "$TARBALL_PATH" "$TARBALL_URL"
SHA256=$(shasum -a 256 "$TARBALL_PATH" | awk '{print $1}')
echo "Source archive sha256: $SHA256"
ruby "$ROOT/scripts/release/render.rb" "$TEMPLATE" "$CANDIDATE" "$VERSION" "$TARBALL_URL" "$SHA256" "$PARSER"
ruby -c "$CANDIDATE" >/dev/null
ruby "$ROOT/scripts/release/check-existing.rb" "$TAP_PATH/Formula/ianchor.rb" "$VERSION" "$PARSER" "$TARBALL_URL" "$SHA256"
check_source_tag

# An API failure is not evidence of absence. First prove repository access.
GH_TOKEN="$SOURCE_TOKEN" gh api "repos/$REPO" --silent
set +e
GH_TOKEN="$SOURCE_TOKEN" gh api --include "repos/$REPO/releases/tags/$TAG" > "$WORK/response" 2> "$WORK/query-error"
QUERY_STATUS=$?
set -e
HTTP_STATUS=$(awk 'NR == 1 {gsub(/\r/, ""); print $2}' "$WORK/response")
if [ "$QUERY_STATUS" -eq 0 ] && [ "$HTTP_STATUS" = 200 ]; then
  ruby "$ROOT/scripts/release/check-release.rb" "$WORK/response" "$TAG"
  echo "Release $TAG already exists; skipping creation."
elif [ "$QUERY_STATUS" -ne 0 ] && [ "$HTTP_STATUS" = 404 ]; then
  GH_TOKEN="$SOURCE_TOKEN" gh release create "$TAG" --repo "$REPO" --verify-tag --title "$TAG" --notes "Release $TAG"
else
  # Classify untrusted stderr without ever printing it (it may contain credentials).
  ruby "$ROOT/scripts/release/classify.rb" "$WORK/query-error" "$QUERY_STATUS" "${HTTP_STATUS:-unknown}"
  exit 1
fi

check_source_tag
cd "$TAP_PATH"
REMOTE_BASE=$(git ls-remote origin refs/heads/main | awk '{print $1}')
if [ "$REMOTE_BASE" != "$BASE" ]; then
  echo "::error::Tap main changed during preparation; rerun after checking the newer version" >&2
  exit 1
fi
mkdir -p Formula
cp "$CANDIDATE" Formula/ianchor.rb
git add -- Formula/ianchor.rb
set +e
git diff --cached --quiet -- Formula/ianchor.rb
DIFF_STATUS=$?
set -e
case "$DIFF_STATUS" in
  0) echo "Formula is unchanged; no commit or push required." ;;
  1)
    git config user.name "github-actions[bot]"
    git config user.email "github-actions[bot]@users.noreply.github.com"
    git commit -m "chore: update formula for ${TAG}" -- Formula/ianchor.rb
    git push origin HEAD:main
    ;;
  *)
    echo "::error::Cannot inspect staged Formula diff (exit=$DIFF_STATUS)" >&2
    exit "$DIFF_STATUS"
    ;;
esac