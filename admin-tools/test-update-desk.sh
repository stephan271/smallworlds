#!/usr/bin/env bash
# Tests for the local Update Desk (admin-tools/update-desk/): its data files,
# the path along Ring 0's verified commits, the overlay pins and one whole
# step against a fake cluster. No network, no cluster.
set -euo pipefail
exec python3 "$(cd "$(dirname "$0")" && pwd)/update-desk/test_desk.py"
