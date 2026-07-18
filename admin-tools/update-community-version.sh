#!/usr/bin/env bash
set -e

# Bumps the pinned smallworlds base tag in a community overlay repo.
#
# Environment (production vs .dev) is auto-detected — see lib/cluster-env.sh.
# The repo dir it guesses (my-community-config for production,
# my-community-dev-config for .dev) is only a default — prepare-community-repo.sh
# lets the operator type any path at setup time, so pass COMMUNITY_REPO_DIR
# explicitly if your overlay repo lives somewhere else.
#
# Usage:
#   ./admin-tools/update-community-version.sh                              # production, latest tag
#   ./admin-tools/update-community-version.sh v1.2.0                       # production, specific tag
#   ENV_EXT=".dev" ./admin-tools/update-community-version.sh               # dev cluster, latest tag
#   ENV_EXT=".dev" ./admin-tools/update-community-version.sh v1.2.0        # dev cluster, specific tag
#   ./admin-tools/update-community-version.sh v1.2.0 /path/to/repo         # explicit repo dir override

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
# Hetzner resource names (and, by convention, sibling overlay repo dirs) use
# the dash form regardless of the DNS/env_ext dot syntax, e.g. "-dev".
ENV_SUFFIX=""
[ "$CLUSTER" != "production" ] && ENV_SUFFIX="-$CLUSTER"
DEFAULT_REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/../my-community${ENV_SUFFIX}-config"
# Find the highest semantic version tag on the smallworlds repo
DEFAULT_VERSION=$(git ls-remote --tags --sort=-v:refname https://github.com/stephan271/smallworlds.git | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n 1 2>/dev/null || echo "main")

NEW_VERSION="${1:-$DEFAULT_VERSION}"
REPO_DIR="${2:-$DEFAULT_REPO_DIR}"

if [ ! -d "$REPO_DIR" ]; then
    echo "Error: Directory $REPO_DIR does not exist."
    echo "Usage: $0 [NEW_VERSION] [COMMUNITY_REPO_DIR]"
    echo "Target environment: '$CLUSTER' (repo dir defaults to $DEFAULT_REPO_DIR)."
    echo "Select the dev cluster with ENV_EXT=\".dev\", or pass COMMUNITY_REPO_DIR explicitly."
    exit 1
fi

echo "Updating $REPO_DIR ('$CLUSTER' cluster) to point to smallworlds version $NEW_VERSION..."

cd "$REPO_DIR"

# Update kustomization.yaml refs
# This matches 'ref=OLD_VERSION' and 'raw.githubusercontent.com/.../OLD_VERSION/'
find . -name "kustomization.yaml" -type f -exec sed -i -E "s#(github\.com/stephan271/smallworlds\.git.*ref=)[^[:space:]]+#\1${NEW_VERSION}#g; s#(raw\.githubusercontent\.com/stephan271/smallworlds/)[^/]+/#\1${NEW_VERSION}/#g" {} +

if [ -n "$(git status --porcelain)" ]; then
    git add .
    git commit -m "Automated update: Bump upstream smallworlds base to $NEW_VERSION"
    git push origin main
    echo "Successfully updated and pushed community repository to use $NEW_VERSION"
else
    echo "No changes needed; community repository is already pointing to $NEW_VERSION."
fi
