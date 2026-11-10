#!/usr/bin/env bash
set -euo pipefail

# Cut a release: tag it and push the tag to origin.
#
#   admin-tools/bump-version.sh patch|minor|major [--security]
#
# The kind says what an Operator gets (docs/adr/0060, design §4.3):
#   patch  only fixes and security fixes; no feature changes, no data migration
#   minor  an application feature update, a database migration, a new application
#   major  something every Operator must act on: a manual step, a breaking
#          rename, a node rebuild
# --security marks a release that carries a security fix. It waives the soak
# (below), and the release notes should flag it.
#
# The Ring 0 gate (design §4.4): a release is cut from the newest commit of
# main that the Canary Follower verified on Ring 0 (`ring0/verified` success)
# at least 72 hours ago, or with --security from the newest verified one at
# all. Never from an unverified commit. In a real emergency
#   SMALLWORLDS_RELEASE_GATE=off SMALLWORLDS_RELEASE_GATE_REASON="<why>"
# cuts from main's head, and the reason is written into the tag.
#
# A release is a tag on a commit *beside* main, not on main itself. On main the
# base's Applications say `targetRevision: HEAD`, so their tenant manifests come
# from whatever main holds at the moment Argo CD looks — which made "pinned to a
# tag" mean only "pinned app list" (see doc/argocd-apps.md). The tagged commit is
# the release commit plus one change: every Application sourced from this
# repository names the tag instead. An overlay pinned to the tag therefore runs
# exactly the tag, and a push to main reaches no cluster until someone bumps an
# overlay.
#
# Overlays that route an Application to their own repository reset its revision
# to the overlay's HEAD (admin-tools/lib/route_revision.py), which
# bump-overlay-release.sh applies from v1.2.44 on.
#
# SMALLWORLDS_RELEASE_PUSH=0 prepares and tags locally without pushing (tests).
# SMALLWORLDS_SOAK_HOURS overrides the 72 hours (tests, and a later re-think).

usage() {
    echo "usage: $0 patch|minor|major [--security]" >&2
    exit 2
}

KIND="${1:-}"
case "$KIND" in patch|minor|major) ;; *) usage ;; esac
SECURITY=0
if [ "${2:-}" = "--security" ]; then SECURITY=1; elif [ -n "${2:-}" ]; then usage; fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_SLUG="${SMALLWORLDS_REPO:-stephan271/smallworlds}"
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

case "$KIND" in
    patch) NEW_TAG="v${MAJOR}.${MINOR}.$((PATCH + 1))" ;;
    minor) NEW_TAG="v${MAJOR}.$((MINOR + 1)).0" ;;
    major) NEW_TAG="v$((MAJOR + 1)).0.0" ;;
esac

# --- The Ring 0 gate --------------------------------------------------------
if [ "${SMALLWORLDS_RELEASE_GATE:-on}" = "off" ]; then
    if [ -z "${SMALLWORLDS_RELEASE_GATE_REASON:-}" ]; then
        echo "SMALLWORLDS_RELEASE_GATE=off needs SMALLWORLDS_RELEASE_GATE_REASON: the tag records why." >&2
        exit 1
    fi
    RELEASE_COMMIT="$(git rev-parse main)"
    GATE_NOTE="Released WITHOUT the Ring 0 gate: ${SMALLWORLDS_RELEASE_GATE_REASON}"
    echo "WARNING: $GATE_NOTE" >&2
else
    gate_args=(--repo "$REPO_SLUG" --soak-hours "${SMALLWORLDS_SOAK_HOURS:-72}")
    [ "$SECURITY" = 1 ] && gate_args+=(--security)
    # One argument per commit, newest first. Thirty is plenty: Ring 0 sees every
    # commit, and unauthenticated GitHub allows 60 status reads an hour.
    # shellcheck disable=SC2046
    if ! verdict="$(python3 "$SCRIPT_DIR/lib/ring0_gate.py" "${gate_args[@]}" \
                      $(git rev-list --first-parent --max-count=30 main))"; then
        echo "Nothing tagged. Wait for Ring 0, or see the header of $0 for an emergency." >&2
        exit 1
    fi
    IFS=$'\t' read -r RELEASE_COMMIT VERIFIED_AT HOURS_GREEN <<<"$verdict"
    GATE_NOTE="Ring 0: verified ${VERIFIED_AT}, ${HOURS_GREEN} h before this release"
fi

KIND_LABEL="$KIND"
[ "$SECURITY" = 1 ] && KIND_LABEL="$KIND, security"
behind="$(git rev-list --count "$RELEASE_COMMIT..main")"

echo "Latest tag: $LATEST_TAG"
echo "Preparing $NEW_TAG ($KIND_LABEL) from $(git rev-parse --short "$RELEASE_COMMIT")" \
     "($behind commit(s) behind main), with the base's Applications pinned to it..."
echo "$GATE_NOTE"

worktree="$(mktemp -d)"
cleanup() {
    git worktree remove --force "$worktree" >/dev/null 2>&1 || true
    rm -rf "$worktree"
}
trap cleanup EXIT
git worktree add --detach --quiet "$worktree" "$RELEASE_COMMIT"

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
    git tag -a "$NEW_TAG" -F - HEAD <<MSG
Release $NEW_TAG ($KIND_LABEL)

Cut from $(git rev-parse "$RELEASE_COMMIT").
$GATE_NOTE
MSG
    echo "Pinned $pinned Applications to $NEW_TAG."
)

if [ "${SMALLWORLDS_RELEASE_PUSH:-1}" = "0" ]; then
    echo "Tagged $NEW_TAG locally (not pushed)."
    exit 0
fi
git push origin "$NEW_TAG"
echo "Successfully created and pushed tag $NEW_TAG"
