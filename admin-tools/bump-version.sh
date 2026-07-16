#!/usr/bin/env bash
set -e

# This script bumps the patch version of the latest semver tag on the main branch,
# tags it, and pushes the tag to origin.

# Fetch latest tags
git fetch --tags origin

LATEST_TAG=$(git tag --sort=-v:refname | grep -Eo '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1)
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
echo "Creating next higher revision tag: $NEW_TAG on main branch..."

git tag -a "$NEW_TAG" -m "Release $NEW_TAG" main
git push origin "$NEW_TAG"

echo "Successfully created and pushed tag $NEW_TAG"
