#!/usr/bin/env bash
# Tests for join, the Member Invitation page (docs/adr/0058).
#
#   1. Both listeners, against a fake Keycloak         — always run
#   2. The join page and the Invitation Desk, walked in a real browser
#                                                       — run when Playwright is
#      installed for e2e-tests/, skipped otherwise
#
# Neither touches a cluster.
set -uo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
tests="$repository_root/infrastructure/kubernetes/tenants/join/tests"
status=0

python3 "$tests/test_join.py" || status=1
python3 "$tests/test_desk.py" || status=1

playwright="$repository_root/e2e-tests/node_modules/playwright"
if [[ ! -d "$playwright" ]]; then
    echo ""
    echo "browser — skipped (Playwright not installed; run 'npm ci' in e2e-tests/)"
    exit "$status"
fi

scratch="$(mktemp -d)"
server_pid=""
cleanup() {
    [[ -n "$server_pid" ]] && kill "$server_pid" 2>/dev/null
    rm -rf "$scratch"
}
trap cleanup EXIT

python3 "$tests/serve.py" > "$scratch/serve.out" 2>&1 &
server_pid=$!
started=""
for _ in $(seq 1 40); do
    started="$(head -1 "$scratch/serve.out")"
    [[ "$started" == \{* ]] && break
    sleep 0.25
done
if [[ "$started" != \{* ]]; then
    echo "join did not start:" >&2
    cat "$scratch/serve.out" >&2
    exit 1
fi

echo ""
PLAYWRIGHT_MODULE="$playwright/index.mjs" node "$tests/test-interface.mjs" "$started" || status=1
echo ""
PLAYWRIGHT_MODULE="$playwright/index.mjs" node "$tests/test-desk.mjs" "$started" || status=1
exit "$status"
