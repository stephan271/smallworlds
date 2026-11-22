#!/usr/bin/env bash
# Tests for the Operator Board (docs/adr/0068): state, links, the copies of
# the Canary Follower's rules, read-only RBAC, packaging and HTTP; then the
# tenant is rendered. No network, no cluster.
set -euo pipefail
repository_root="$(cd "$(dirname "$0")/.." && pwd)"
tenant="$repository_root/infrastructure/kubernetes/tenants/operator-board"
python3 "$tenant/tests/test_board.py"
if command -v kubectl >/dev/null; then
    kubectl kustomize "$tenant" >/dev/null
    echo "operator-board renders"
else
    echo "render — skipped (kubectl not installed)"
fi
