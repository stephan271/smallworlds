#!/usr/bin/env bash
# The whole invitation path against a real Keycloak (docs/adr/0059).
#
# Starts the Keycloak version the cluster runs, in Docker, with this
# repository's realm and Keycloak extension (action token handler + login
# theme), points join at it, issues an invitation, and walks it in Chromium
# with a virtual passkey: join page, passkey, recovery codes, back to join
# signed in. Nothing here touches a cluster.
#
#   tests/live-keycloak.sh [path/to/action-token-generator.jar] [--keep]
#
# The jar defaults to the one the cluster deploys
# (tenants/keycloak/action-token-generator.jar). Screenshots of every page land
# in $SCREENSHOTS (default: a temporary directory, printed at the end).
# Needs docker, python3 and Playwright from e2e-tests/ (npm ci there).
set -euo pipefail

tests="$(cd "$(dirname "$0")" && pwd)"
tenant="$(dirname "$tests")"
repository_root="$(cd "$tenant/../../../.." && pwd)"
jar="$repository_root/infrastructure/kubernetes/tenants/keycloak/action-token-generator.jar"
keep=""
for arg in "$@"; do
    case "$arg" in
        --keep) keep=1 ;;
        *) jar="$(cd "$(dirname "$arg")" && pwd)/$(basename "$arg")" ;;
    esac
done

image="quay.io/keycloak/keycloak:$(sed -n 's/^  tag: "\(.*\)"/\1/p' "$repository_root/infrastructure/kubernetes/tenants/keycloak/values.yaml")"
playwright="$repository_root/e2e-tests/node_modules/playwright"
container="sw-join-live-keycloak"
kc_port=18080
join_port=18090
desk_port=18091
secret="live-keycloak-secret"

scratch="$(mktemp -d)"
screenshots="${SCREENSHOTS:-$scratch/screenshots}"
mkdir -p "$screenshots"
join_pid=""
cleanup() {
    [[ -n "$join_pid" ]] && kill "$join_pid" 2>/dev/null
    if [[ -z "$keep" ]]; then
        docker rm -f "$container" >/dev/null 2>&1 || true
    else
        echo "Keycloak left running: http://localhost:$kc_port (admin/admin); docker rm -f $container"
    fi
}
trap cleanup EXIT

[[ -d "$playwright" ]] || { echo "Playwright is not installed: run 'npm ci' in e2e-tests/." >&2; exit 2; }

# The realm as realm-config-job imports it, for a Keycloak on localhost: the
# WebAuthn relying party is the host the browser sees.
sed -e 's/\${env.IDENTITY_HOST}/localhost/g' -e 's/\${env.STALWART_PASSWORD}/unused/g' \
    "$repository_root/infrastructure/kubernetes/tenants/keycloak/smallworlds-realm.json" > "$scratch/realm.json"
chmod 644 "$scratch/realm.json"

docker rm -f "$container" >/dev/null 2>&1 || true
docker run -d --name "$container" -p "127.0.0.1:$kc_port:8080" \
    -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
    -v "$jar:/opt/keycloak/providers/action-token-generator.jar:ro,Z" \
    -v "$scratch/realm.json:/tmp/realm.json:ro,Z" \
    "$image" start-dev --features=recovery-codes >/dev/null

echo "Waiting for Keycloak ($image)..."
for _ in $(seq 1 120); do
    curl -sf "http://localhost:$kc_port/realms/master" >/dev/null && break
    sleep 2
done
curl -sf "http://localhost:$kc_port/realms/master" >/dev/null || { docker logs "$container" | tail -40; exit 1; }

kcadm() { docker exec "$container" /opt/keycloak/bin/kcadm.sh "$@"; }
kcadm config credentials --server http://localhost:8080 --realm master --user admin --password admin >/dev/null
kcadm create realms -f /tmp/realm.json >/dev/null
# What realm-config-job sets on every sync.
kcadm update realms/smallworlds -s registrationAllowed=false -s internationalizationEnabled=true \
    -s 'supportedLocales=["de","en"]' -s defaultLocale=en -s loginTheme=smallworlds
# What keycloak-client-job and join-keycloak-setup-job create.
kcadm create clients -r smallworlds -s clientId=join -s enabled=true -s publicClient=false \
    -s standardFlowEnabled=true -s serviceAccountsEnabled=true -s secret="$secret" \
    -s "redirectUris=[\"http://localhost:$join_port/*\"]" >/dev/null
kcadm add-roles -r smallworlds --uusername service-account-join --cclientid realm-management \
    --rolename manage-users --rolename view-users --rolename query-users

export KEYCLOAK_URL="http://localhost:$kc_port" PUBLIC_IDENTITY_URL="http://localhost:$kc_port"
export KEYCLOAK_CLIENT_SECRET="$secret" JOIN_URL="http://localhost:$join_port"
export DASHBOARD_URL="http://localhost:$join_port/dashboard-stand-in"
export DB_PATH="$scratch/invitations.db" STATIC_DIR="$tenant/static" BIND=127.0.0.1
export PUBLIC_PORT="$join_port" DESK_PORT="$desk_port" PYTHONPATH="$tenant/src"
python3 "$tenant/src/main.py" > "$scratch/join.log" 2>&1 &
join_pid=$!
for _ in $(seq 1 40); do curl -sf "http://localhost:$join_port/healthz" >/dev/null && break; sleep 0.25; done

new_code() { python3 "$tenant/src/manage.py" create --locale "$1" --label "$2" | sed -n 's/^Code: //p'; }
codes="{\"de\":\"$(new_code de Anna)\",\"en\":\"$(new_code en Ben)\"}"

echo ""
status=0
PLAYWRIGHT_MODULE="$playwright/index.mjs" node "$tests/test-live-keycloak.mjs" \
    "{\"keycloak\":\"http://localhost:$kc_port\",\"join\":\"http://localhost:$join_port\",\"codes\":$codes,\"screenshots\":\"$screenshots\"}" \
    || status=1
echo ""
echo "Screenshots: $screenshots"
[[ $status -eq 0 ]] || { echo "--- join log"; tail -20 "$scratch/join.log"; echo "--- keycloak log"; docker logs "$container" 2>&1 | grep -E "WARN|ERROR" | tail -20; }
exit $status
