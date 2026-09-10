#!/usr/bin/env bash
# Create and push a release tag for the current companion-mod/info.json version.
# Triggers .github/workflows/release.yml on push.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

err() { echo "release.sh: $*" >&2; exit 1; }

[[ -f companion-mod/info.json ]]    || err "companion-mod/info.json not found (run from repo root or via tools/release.sh)"
[[ -f companion-mod/changelog.txt ]] || err "companion-mod/changelog.txt not found"
command -v jq >/dev/null  || err "jq required"
command -v git >/dev/null || err "git required"

NAME=$(jq -r .name companion-mod/info.json)
VERSION=$(jq -r .version companion-mod/info.json)
TAG="v${VERSION}"

# Working tree must be clean, the tag should point at a real, pushed commit.
if [[ -n "$(git status --porcelain)" ]]; then
    err "working tree is dirty; commit or stash before tagging"
fi

# Soft branch check, warn but do not block.
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$BRANCH" != "master" && "$BRANCH" != "main" ]]; then
    echo "warning: tagging from branch '$BRANCH' (not master/main)"
fi

# changelog.txt must contain an entry for this version.
if ! grep -qE "^Version: ${VERSION//./\\.}\$" companion-mod/changelog.txt; then
    err "companion-mod/changelog.txt has no 'Version: ${VERSION}' entry, run the bump-version skill first"
fi

# Tag must not already exist locally.
if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
    err "tag ${TAG} already exists locally (delete with 'git tag -d ${TAG}' if intended)"
fi

# Tag must not already exist on origin.
echo "fetching tags from origin..."
git fetch --tags --quiet origin
if git ls-remote --tags --exit-code origin "refs/tags/${TAG}" >/dev/null 2>&1; then
    err "tag ${TAG} already exists on origin, version already released"
fi

# The tag points at HEAD, so HEAD must be reachable on origin (i.e. pushed).
if ! git merge-base --is-ancestor HEAD "@{upstream}" 2>/dev/null; then
    UPSTREAM=$(git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || echo "")
    if [[ -z "$UPSTREAM" ]]; then
        err "current branch has no upstream, push it first so the tag is reachable on origin"
    fi
    err "HEAD is ahead of ${UPSTREAM}, push commits before tagging (tag points at HEAD)"
fi

cat <<EOF
About to release:
  project: ${NAME}
  version: ${VERSION}
  tag:     ${TAG}
  commit:  $(git rev-parse --short HEAD)  ($(git log -1 --pretty=%s))

This will:
  1. Create annotated tag ${TAG}
  2. Push tag to origin (triggers the GitHub Actions release workflow)
EOF

read -r -p "Proceed? [y/N] " reply
[[ "$reply" =~ ^[Yy]$ ]] || err "aborted"

git tag -a "${TAG}" -m "Release ${VERSION}"
git push origin "${TAG}"

REMOTE_URL=$(git config --get remote.origin.url || echo "")
if [[ "$REMOTE_URL" =~ github.com[:/](.+)\.git$ ]]; then
    SLUG="${BASH_REMATCH[1]}"
    echo ""
    echo "Tag pushed. Watch the build:"
    echo "  https://github.com/${SLUG}/actions"
    echo "Release will appear at:"
    echo "  https://github.com/${SLUG}/releases/tag/${TAG}"
fi
