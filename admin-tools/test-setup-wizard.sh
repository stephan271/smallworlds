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

echo ""
# The interface is walked against a wizard told to look at an empty machine, so
# the assertions are about a first-time operator rather than about whatever this
# developer happens to have cached.
log="$(mktemp)"
SMALLWORLDS_COMMUNITY_CONFIG=/nonexistent/community.conf \
SMALLWORLDS_INIT_CACHE=/nonexistent/init.env \
    python3 "$wizard/wizard.py" --no-browser --port 0 > "$log" 2>&1 &
wizard_pid=$!
trap 'kill "$wizard_pid" 2>/dev/null; rm -f "$log"' EXIT

url=""
for _ in $(seq 1 40); do
    url="$(grep -o 'http://127\.0\.0\.1:[0-9]*/?t=[A-Za-z0-9_-]*' "$log" | head -1)"
    [[ -n "$url" ]] && break
    sleep 0.25
done
if [[ -z "$url" ]]; then
    echo "The wizard did not start:"
    cat "$log"
    exit 1
fi

PLAYWRIGHT_MODULE="$playwright/index.mjs" \
    node "$wizard/test-interface.mjs" "$url" || status=1
exit "$status"
