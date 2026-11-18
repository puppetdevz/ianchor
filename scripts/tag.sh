#!/bin/bash
set -euo pipefail

# Usage: ./scripts/tag.sh [patch|minor|major|resume]
# Default: patch. Use resume after a failed push, without creating a new bump.
#
# VERSION is the single source of truth for the release version. Tags are
# immutable: an existing tag is never deleted or moved.

BUMP="${1:-patch}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

case "$BUMP" in
  patch|minor|major|resume) ;;
  *)
    echo "Error: expected patch, minor, major or resume (got: ${BUMP})"
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

# Ensure the tracked working tree is clean. Untracked files cannot change the
# commit the tag points at, and publish.sh applies the same rule; a stray local
# note must not block a release.
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "Error: working tree is not clean. Commit or stash changes first."
  exit 1
fi

if [ "$BUMP" = "resume" ]; then
  TAG="v${CURRENT}"
  if [ "$(git log -1 --format=%s)" != "chore: bump version to ${TAG}" ]; then
    echo "Error: HEAD is not the version bump commit for ${TAG}; refusing to resume."
    exit 1
  fi
  if git rev-parse -q --verify "refs/tags/${TAG}" > /dev/null; then
    if [ "$(git rev-parse "refs/tags/${TAG}^{commit}")" != "$(git rev-parse HEAD)" ]; then
      echo "Error: tag ${TAG} does not point to HEAD. Tags are immutable."
      exit 1
    fi
  else
    git tag "$TAG"
  fi
  echo "==> Resuming ${TAG}..."
  git push origin main
  git push origin "$TAG"
  echo "==> Done! ${TAG} pushed. GitHub Actions will handle the release."
  exit 0
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
echo "    If a push fails, rerun: ./scripts/tag.sh resume"
git push origin main

echo "==> Creating tag ${TAG}..."
git tag "$TAG"
git push origin "$TAG"

echo "==> Done! ${TAG} pushed. GitHub Actions will handle the release."
echo "==> After GitHub Actions completes, run: brew update && brew upgrade ianchor"
