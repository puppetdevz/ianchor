#!/bin/bash
set -euo pipefail

# Usage: ./scripts/tag.sh [patch|minor|major]
# Default: patch
#
# VERSION is the single source of truth for the release version. Tags are
# immutable: an existing tag is never deleted or moved.

BUMP="${1:-patch}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

case "$BUMP" in
  patch|minor|major) ;;
  *)
    echo "Error: expected patch, minor or major (got: ${BUMP})"
    exit 1
    ;;
esac

CURRENT=$(tr -d '[:space:]' < VERSION)

# Ensure we're on main
BRANCH=$(git branch --show-current)
if [ "$BRANCH" != "main" ]; then
  echo "Error: not on main branch (current: ${BRANCH})"
  exit 1
fi

# Ensure working tree is clean
if [ -n "$(git status --porcelain)" ]; then
  echo "Error: working tree is not clean. Commit or stash changes first."
  exit 1
fi

IFS=. read -r MAJOR MINOR PATCH <<< "$CURRENT"
case "$BUMP" in
  major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
  minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
  patch) PATCH=$((PATCH + 1)) ;;
esac
VERSION="${MAJOR}.${MINOR}.${PATCH}"
TAG="v${VERSION}"

if git rev-parse -q --verify "refs/tags/${TAG}" > /dev/null; then
  echo "Error: tag ${TAG} already exists. Tags are immutable; bump to a new version instead."
  exit 1
fi

echo "==> ${CURRENT} -> ${VERSION} (${BUMP})"
printf '%s\n' "$VERSION" > VERSION

git add VERSION
git commit -m "chore: bump version to ${TAG}"

echo "==> Pushing to origin..."
git push origin main

echo "==> Creating tag ${TAG}..."
git tag "$TAG"
git push origin "$TAG"

echo "==> Done! ${TAG} pushed. GitHub Actions will handle the release."
echo "==> After GitHub Actions completes, run: brew update && brew upgrade ianchor"