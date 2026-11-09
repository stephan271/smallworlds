#!/usr/bin/env bash
set -euo pipefail

# Cut the next patch release: tag it and push the tag to origin.
#
# A release is a tag on a commit *beside* main, not on main itself. On main the
# base's Applications say `targetRevision: HEAD`, so their tenant manifests come
# from whatever main holds at the moment Argo CD looks — which made "pinned to a
# tag" mean only "pinned app list" (see doc/argocd-apps.md). The tagged commit is
# main plus one change: every Application sourced from this repository names
# the tag instead. An overlay pinned to the tag therefore runs exactly the tag,
# and a push to main reaches no cluster until someone bumps an overlay.
#
# Overlays that route an Application to their own repository reset its revision
# to the overlay's HEAD (admin-tools/lib/route_revision.py), which
# bump-overlay-release.sh applies from v1.2.44 on.
#
# SMALLWORLDS_RELEASE_PUSH=0 prepares and tags locally without pushing (tests).

BASE_REPO_PATTERN="repoURL: ['\"]\\?https://github.com/stephan271/smallworlds.git['\"]\\?\$"

git fetch --tags origin

LATEST_TAG=$(git tag --sort=-v:refname | grep -Eo '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)
if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.0.0"
fi

if [[ $LATEST_TAG =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    MAJOR="${BASH_REMATCH[1]}"
    MINOR="${BASH_REMATCH[2]}"
    PATCH="${BASH_REMATCH[3]}"
else
    echo "Could not parse latest tag: $LATEST_TAG"
    exit 1
fi

NEW_PATCH=$((PATCH + 1))
NEW_TAG="v${MAJOR}.${MINOR}.${NEW_PATCH}"

echo "Latest tag: $LATEST_TAG"
echo "Preparing $NEW_TAG from main, with the base's Applications pinned to it..."

worktree="$(mktemp -d)"
cleanup() {
    git worktree remove --force "$worktree" >/dev/null 2>&1 || true
    rm -rf "$worktree"
}
trap cleanup EXIT
git worktree add --detach --quiet "$worktree" main

(
    cd "$worktree"
    pinned=0
    for file in infrastructure/kubernetes/apps/*.yaml; do
        grep -q "$BASE_REPO_PATTERN" "$file" || continue
        grep -q 'targetRevision: HEAD$' "$file" || continue
        sed -i "s|targetRevision: HEAD\$|targetRevision: $NEW_TAG|" "$file"
        pinned=$((pinned + 1))
    done
    left="$(grep -l "$BASE_REPO_PATTERN" infrastructure/kubernetes/apps/*.yaml \
              | xargs grep -l 'targetRevision: HEAD' || true)"
    if [ -n "$left" ]; then
        echo "Still tracking HEAD after pinning: $left" >&2
        exit 1
    fi
    if [ "$pinned" -eq 0 ]; then
        echo "No Application tracked HEAD — nothing to pin, which is unexpected." >&2
        exit 1
    fi
    git commit --quiet -am "Release $NEW_TAG: pin the base's Applications to $NEW_TAG"
    git tag -a "$NEW_TAG" -m "Release $NEW_TAG" HEAD
    echo "Pinned $pinned Applications to $NEW_TAG."
)

if [ "${SMALLWORLDS_RELEASE_PUSH:-1}" = "0" ]; then
    echo "Tagged $NEW_TAG locally (not pushed)."
    exit 0
fi
git push origin "$NEW_TAG"
echo "Successfully created and pushed tag $NEW_TAG"
