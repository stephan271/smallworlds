#!/bin/bash
# Builds the SmallWorlds Keycloak extension and installs it where the cluster
# mounts it from (infrastructure/kubernetes/tenants/keycloak/):
#   - the action-token-link endpoint (bulk-invite.py, join)
#   - join's action token and its handler (docs/adr/0059)
#   - the `smallworlds` login theme (docs/adr/0059)
#
# Maven runs in Docker, so no local JDK or Maven is needed. The Keycloak
# version in pom.xml must match the image tag in the tenant's values.yaml.
# Check the result against a real Keycloak before committing:
#   infrastructure/kubernetes/tenants/join/tests/live-keycloak.sh
set -euo pipefail
cd "$(dirname "$0")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -r pom.xml src "$work/"
mkdir -p "$HOME/.m2"
docker run --rm -u "$(id -u):$(id -g)" -e MAVEN_CONFIG=/var/maven/.m2 \
    -v "$HOME/.m2:/var/maven/.m2:Z" -v "$work:/src:Z" -w /src \
    docker.io/library/maven:3.9.9-eclipse-temurin-21 \
    mvn -q -B -Duser.home=/var/maven package

tenant="../../kubernetes/tenants/keycloak"
cp "$work"/target/action-token-generator-*.jar "$tenant/action-token-generator.jar"

# Keycloak loads providers only at start-up and the jar's ConfigMap keeps its
# name, so only a changed pod annotation gets a new jar into the running pod.
sum="$(sha256sum "$tenant/action-token-generator.jar" | cut -d' ' -f1)"
sed -i "s|^\(  smallworlds/keycloak-extension-sha256: \).*|\1\"$sum\"|" "$tenant/values.yaml"
grep -q "$sum" "$tenant/values.yaml" || { echo "values.yaml has no keycloak-extension-sha256 annotation" >&2; exit 1; }
echo "Installed $tenant/action-token-generator.jar (sha256 $sum) and updated values.yaml."
