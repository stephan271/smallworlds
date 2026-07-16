#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# SmallWorlds E2E Smoke Test Runner
#
# Runs browser-based smoke tests against a live SmallWorlds community.
# Tests simulate real users logging in and using applications.
#
# Usage:
#   ./e2e-tests/run-smoke-tests.sh <domain> [keycloak-admin-password]
#
# If keycloak-admin-password is not provided, the script will attempt to
# read it from the cluster via kubectl.
#
# Examples:
#   ./e2e-tests/run-smoke-tests.sh smallworlds.network
#   ./e2e-tests/run-smoke-tests.sh smallworlds.network MyAdminPass123
#
# Environment variables (override CLI args):
#   DOMAIN          - Target domain
#   KC_ADMIN_PASS   - Keycloak admin password
#   HEADED          - Set to "1" for headed browser mode
#   SLOWMO          - Slow down operations (ms), e.g. "500"
#   SKIP_PROVISION  - Set to "1" to skip user provisioning
#   FULL_OIDC       - Set to "1" to run full OIDC login roundtrips (requires
#                     certificates the APPS trust, i.e. production; staging
#                     runs shallow OIDC wiring checks instead)
#   KUBECONFIG      - Path to kubeconfig file (default:
#                     ~/.smallworlds/kubeconfigs/<production|dev>.yaml,
#                     per the configured env_ext / ENV_EXT)
# ============================================================================

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Parse arguments ---
export DOMAIN="${DOMAIN:-${1:-}}"
export KC_ADMIN_PASS="${KC_ADMIN_PASS:-${2:-}}"
export APP_FILTER="${APP_FILTER:-${3:-}}"

if [[ -z "$DOMAIN" ]]; then
  echo -e "${RED}Usage: $0 <domain> [keycloak-admin-password] [app-filter]${NC}"
  echo -e "  e.g.: $0 smallworlds.network"
  exit 1
fi

echo -e "${CYAN}╔══════════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║       SmallWorlds E2E Smoke Test Runner             ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  Domain:  ${YELLOW}${DOMAIN}${NC}"

# --- Resolve Keycloak admin password ---
if [[ -z "$KC_ADMIN_PASS" ]]; then
  echo -e "  ${YELLOW}No admin password provided. Trying kubectl...${NC}"
  source "$(dirname "$SCRIPT_DIR")/admin-tools/lib/cluster-env.sh"
  KUBECONFIG="${KUBECONFIG:-$(kubeconfig_path "$(cluster_label "$(detect_env_ext)")")}"

  if [[ -f "$KUBECONFIG" ]]; then
    export KUBECONFIG
    KC_ADMIN_PASS=$(kubectl -n keycloak get secret keycloak-admin-creds \
      -o jsonpath='{.data.admin-password}' 2>/dev/null | base64 -d 2>/dev/null) || true
  fi

  if [[ -z "$KC_ADMIN_PASS" ]]; then
    echo -e "${RED}❌ Could not retrieve Keycloak admin password.${NC}"
    echo -e "   Please provide it as the second argument or set KC_ADMIN_PASS."
    exit 1
  fi
  echo -e "  ${GREEN}✅ Retrieved admin password from cluster${NC}"
fi
export KC_ADMIN_PASS

echo ""

# --- Step 1: Check dependencies ---
echo -e "${CYAN}[1/4] Checking dependencies...${NC}"
cd "$SCRIPT_DIR"

if [[ ! -d "node_modules" ]]; then
  echo "  Installing npm dependencies..."
  npm install --silent 2>&1 | tail -1
fi

if ! npx playwright install chromium --dry-run &>/dev/null 2>&1; then
  echo "  Installing Playwright browsers..."
  npx playwright install chromium 2>&1 | tail -3
fi
echo -e "  ${GREEN}✅ Dependencies ready${NC}"
echo ""

# --- Step 2: Wait for services ---
echo -e "${CYAN}[2/4] Checking service availability...${NC}"

check_service() {
  local name="$1"
  local url="$2"
  local max_retries=12
  local count=0

  while [[ $count -lt $max_retries ]]; do
    status=$(curl -sSk -o /dev/null -w "%{http_code}" --max-time 10 "$url" 2>/dev/null) || status="000"
    if [[ "$status" =~ ^(200|301|302|303|307|401|403)$ ]]; then
      echo -e "  ${GREEN}✅ ${name} is up (HTTP ${status})${NC}"
      return 0
    fi
    echo -e "  ${YELLOW}⏳ ${name}: HTTP ${status}, retrying in 10s... (${count}/${max_retries})${NC}"
    sleep 10
    count=$((count + 1))
  done

  echo -e "  ${RED}❌ ${name} is not responding after ${max_retries} retries${NC}"
  return 1
}

SERVICES_OK=true
check_service "Keycloak"  "https://identity.${DOMAIN}/" || SERVICES_OK=false

if [[ -z "$APP_FILTER" || "$APP_FILTER" == *"nextcloud"* ]]; then
  check_service "Nextcloud" "https://files.${DOMAIN}/"    || SERVICES_OK=false
fi
if [[ -z "$APP_FILTER" || "$APP_FILTER" == *"bulwark"* ]]; then
  check_service "Bulwark"   "https://webmail.${DOMAIN}/"   || SERVICES_OK=false
fi
if [[ -z "$APP_FILTER" || "$APP_FILTER" == *"immich"* ]]; then
  check_service "Immich"    "https://photos.${DOMAIN}/"    || SERVICES_OK=false
fi
if [[ -z "$APP_FILTER" || "$APP_FILTER" == *"forgejo"* ]]; then
  check_service "Forgejo"   "https://git.${DOMAIN}/"       || SERVICES_OK=false
fi
if [[ -z "$APP_FILTER" || "$APP_FILTER" == *"jitsi"* ]]; then
  check_service "Jitsi"     "https://meet.${DOMAIN}/"      || SERVICES_OK=false
fi

if [[ "$SERVICES_OK" != "true" ]]; then
  echo -e "\n${RED}⚠ Some services are not available. Tests may fail.${NC}"
  echo -e "${YELLOW}Continuing anyway...${NC}\n"
fi
echo ""

# --- Step 3: Provision test users ---
if [[ "${SKIP_PROVISION:-}" != "1" ]]; then
  echo -e "${CYAN}[3/4] Provisioning test users...${NC}"
  npx tsx setup/provision-test-users.ts
else
  echo -e "${CYAN}[3/4] Skipping user provisioning (SKIP_PROVISION=1)${NC}"
fi
echo ""

# --- Step 4: Run tests ---
echo -e "${CYAN}[4/4] Running smoke tests...${NC}"
echo ""

PLAYWRIGHT_ARGS=""
if [[ "${HEADED:-}" == "1" ]]; then
  PLAYWRIGHT_ARGS="--headed"
fi

if [[ -n "$APP_FILTER" ]]; then
  PLAYWRIGHT_ARGS="$PLAYWRIGHT_ARGS $APP_FILTER"
fi

# Run Playwright tests
set +e
npx playwright test $PLAYWRIGHT_ARGS
TEST_EXIT_CODE=$?
set -e

echo ""

# --- Report results ---
if [[ $TEST_EXIT_CODE -eq 0 ]]; then
  echo -e "${GREEN}╔══════════════════════════════════════════════════════╗${NC}"
  echo -e "${GREEN}║            All smoke tests passed! ✅               ║${NC}"
  echo -e "${GREEN}╚══════════════════════════════════════════════════════╝${NC}"
else
  echo -e "${RED}╔══════════════════════════════════════════════════════╗${NC}"
  echo -e "${RED}║         Some smoke tests failed! ❌                 ║${NC}"
  echo -e "${RED}╚══════════════════════════════════════════════════════╝${NC}"
fi

echo ""
echo -e "  HTML report: ${CYAN}${SCRIPT_DIR}/reports/html/index.html${NC}"
echo -e "  View with:   ${YELLOW}cd e2e-tests && npx playwright show-report reports/html${NC}"
echo ""

exit $TEST_EXIT_CODE
