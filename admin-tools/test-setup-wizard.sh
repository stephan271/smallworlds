#!/usr/bin/env bash
# Tests for the browser setup wizard (admin-tools/setup-wizard/).
#
#   1. Its server, defaults and dry-run plan       — always run
#   2. Its interface, walked in a real browser     — run when Playwright is
#      installed for e2e-tests/, skipped otherwise
#
# Neither creates anything: Phase 1 of the wizard explains and does not act.
set -uo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
wizard="$repository_root/admin-tools/setup-wizard"
status=0

python3 "$wizard/test_wizard.py" || status=1

playwright="$repository_root/e2e-tests/node_modules/playwright"
if [[ ! -d "$playwright" ]]; then
    echo ""
    echo "interface — skipped (Playwright not installed; run 'npm ci' in e2e-tests/)"
    exit "$status"
fi

scratch="$(mktemp -d)"
pids=()
cleanup() {
    for pid in ${pids+"${pids[@]}"}; do kill "$pid" 2>/dev/null; done
    rm -rf "$scratch"
}
trap cleanup EXIT

# Starts a wizard with the given environment and prints its URL. Always told to
# look at an empty machine, so the assertions are about a first-time operator
# rather than about whatever this developer happens to have cached.
start_wizard() {
    local log="$scratch/wizard-$$-${#pids[@]}.log"
    env "$@" \
        SMALLWORLDS_COMMUNITY_CONFIG="$scratch/community-${#pids[@]}.conf" \
        SMALLWORLDS_INIT_CACHE="$scratch/cache-${#pids[@]}.env" \
        python3 "$wizard/wizard.py" --no-browser --port 0 > "$log" 2>&1 &
    pids+=($!)
    local url=""
    for _ in $(seq 1 60); do
        url="$(grep -o 'http://127\.0\.0\.1:[0-9]*/?t=[A-Za-z0-9_-]*' "$log" | head -1)"
        [[ -n "$url" ]] && break
        sleep 0.25
    done
    if [[ -z "$url" ]]; then
        echo "The wizard did not start:" >&2
        cat "$log" >&2
        return 1
    fi
    printf '%s' "$url"
}

echo ""
# 1. The five panels, against this checkout — plan only, nothing is installed.
url="$(start_wizard SMALLWORLDS_NOOP=1)" || exit 1
PLAYWRIGHT_MODULE="$playwright/index.mjs" \
    node "$wizard/test-interface.mjs" "$url" plan || status=1

echo ""
# 2. Clicking Install, against a checkout whose two scripts print markers and
# exit. This walks the whole browser path without touching a cloud account.
checkout="$(cd "$wizard" && python3 -c '
import sys, test_wizard
print(test_wizard.make_stub_checkout(sys.argv[1]))' "$scratch")"
url="$(start_wizard SMALLWORLDS_REPO_ROOT="$checkout")" || exit 1
PLAYWRIGHT_MODULE="$playwright/index.mjs" \
    node "$wizard/test-interface.mjs" "$url" install || status=1

exit "$status"
