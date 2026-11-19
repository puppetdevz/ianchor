#!/bin/bash
set -euo pipefail

# VERSION and release tags are immutable once pushed. A failed push can have
# succeeded remotely; resume always inspects the destination before retrying.
[ "$#" -le 1 ] || { echo "Error: expected at most one argument"; exit 1; }
BUMP="${1:-patch}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
case "$BUMP" in patch|minor|major|resume) ;; *) echo "Error: expected patch, minor, major or resume (got: ${BUMP})"; exit 1 ;; esac
CURRENT=$(ruby -e 's=File.binread("VERSION"); abort "Error: invalid VERSION" unless /\A(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\n?\z/.match?(s); print s.strip')
BRANCH=$(git branch --show-current)
[ "$BRANCH" = main ] || { echo "Error: not on main branch (current: ${BRANCH})"; exit 1; }
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "Error: working tree is not clean. Commit or stash changes first."; exit 1; }
HEAD_COMMIT=$(git rev-parse HEAD)
if [ "$BUMP" = resume ]; then
  TAG="v${CURRENT}"
  [ "$(git log -1 --format=%s)" = "chore: bump version to ${TAG}" ] || { echo "Error: HEAD is not the version bump commit for ${TAG}; refusing to resume."; exit 1; }
  BASE=$(git rev-parse 'HEAD^')
  [ "$(git diff-tree --no-commit-id --name-only -r HEAD)" = VERSION ] || { echo "Error: bump commit must change only VERSION"; exit 1; }
else
  VERSION=$(ruby -e 'v=ARGV[0].split(".").map(&:to_i); i={"major"=>0,"minor"=>1,"patch"=>2}.fetch(ARGV[1]); v[i]+=1; ((i+1)...3).each { |n| v[n]=0 }; puts v.join(".")' "$CURRENT" "$BUMP")
  TAG="v${VERSION}"
  BASE=$HEAD_COMMIT
  if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
    echo "Error: tag ${TAG} already exists. Tags are immutable."
    exit 1
  fi
fi
DEST=$(git remote get-url --push --all origin)
case "$DEST" in *$'\n'*|'') echo "Error: exactly one origin push URL is required"; exit 1 ;; esac
REFS=$(git ls-remote "$DEST" "refs/heads/main" "refs/tags/$TAG" "refs/tags/$TAG^{}") || { echo "Error: cannot inspect push destination"; exit 1; }
REMOTE_MAIN=$(printf '%s\n' "$REFS" | awk '$2 == "refs/heads/main" {print $1}')
REMOTE_TAG=$(printf '%s\n' "$REFS" | awk -v ref="refs/tags/$TAG" '$2 == ref {print $1}')
REMOTE_PEELED=$(printf '%s\n' "$REFS" | awk -v ref="refs/tags/$TAG^{}" '$2 == ref {print $1}')
if [ "$BUMP" = resume ]; then
  if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null && [ "$(git rev-parse "refs/tags/${TAG}^{commit}")" != "$HEAD_COMMIT" ]; then
    echo "Error: tag ${TAG} does not point to HEAD. Tags are immutable."; exit 1
  fi
  if [ -n "$REMOTE_TAG" ]; then
    if [ "${REMOTE_PEELED:-$REMOTE_TAG}" = "$HEAD_COMMIT" ] && [ "$REMOTE_MAIN" = "$HEAD_COMMIT" ]; then
      echo "${TAG} is already present remotely; no new tag event will be generated."
      exit 0
    fi
    echo "Error: remote tag already exists or remote main conflicts; inspect manually."; exit 1
  fi
  # An already-up-to-date branch is omitted by git push, so its lease would
  # not protect a legacy branch-only partial push against a concurrent rewind.
  [ "$REMOTE_MAIN" = "$BASE" ] || { echo "Error: remote main conflicts or reflects a legacy partial push; inspect manually."; exit 1; }
else
  [ -z "$REMOTE_TAG" ] || { echo "Error: remote tag ${TAG} already exists."; exit 1; }
  [ "$REMOTE_MAIN" = "$BASE" ] || { echo "Error: remote main does not match HEAD."; exit 1; }
  echo "==> ${CURRENT} -> ${VERSION} (${BUMP})"
  printf '%s\n' "$VERSION" > VERSION
  git add VERSION
  git commit -m "chore: bump version to ${TAG}"
fi
if ! git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then git tag "$TAG"; fi
echo "==> Atomically pushing main and ${TAG}..."
if ! git push --atomic --force-with-lease="refs/heads/main:$REMOTE_MAIN" "$DEST" "refs/heads/main:refs/heads/main" "refs/tags/${TAG}:refs/tags/${TAG}"; then
  echo "Error: push failed or its outcome is unknown. Inspect the remote, then rerun: ./scripts/tag.sh resume"
  exit 1
fi
echo "==> Done! ${TAG} pushed. GitHub Actions will handle the release."
