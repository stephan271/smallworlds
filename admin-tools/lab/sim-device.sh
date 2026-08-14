#!/usr/bin/env bash
# Simulate a member's home device (the Raspberry Pi 5 reference build) on the
# operator laptop.
#
# admin-tools/pod-device/install.sh needs root, systemd and a dedicated system
# user; none of that is the protocol. pod-agent.py takes --config/--data (and
# POD_CONFIG/POD_DATA), so the device is faithfully simulated by running the
# same unmodified agent as an ordinary user against its own data directory.
# What the simulation preserves: outbound-only HTTP, read-only device token,
# digest verification, hash-chain checking, and no delete path.
#
# Usage:
#   ./sim-device.sh enrol '<enrolment-string>'   write the device config
#   ./sim-device.sh sync                         one pull (what the timer runs)
#   ./sim-device.sh verify                       re-hash the whole local copy
#   ./sim-device.sh status                       what the device holds
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEVICE_HOME="${DEVICE_HOME:-$HOME/.smallworlds/lab-device}"
AGENT="$REPO_ROOT/admin-tools/pod-device/pod-agent.py"

export POD_CONFIG="$DEVICE_HOME/config.json"
export POD_DATA="$DEVICE_HOME/data"

mkdir -p "$DEVICE_HOME" "$POD_DATA/objects"
chmod 700 "$DEVICE_HOME"

case "${1:-}" in
    enrol)
        [[ $# -eq 2 ]] || { echo "Usage: $0 enrol '<enrolment-string>'" >&2; exit 1; }
        # Same decode install.sh performs, minus the system user and units.
        umask 077
        python3 - "$2" "$POD_CONFIG" <<'PY'
import base64, json, sys
document = json.loads(base64.b64decode(sys.argv[1]))
missing = [k for k in ("url", "user_id", "token") if not document.get(k)]
if missing:
    sys.exit(f"Enrolment string is missing: {', '.join(missing)}")
with open(sys.argv[2], "w") as handle:
    json.dump(document, handle, indent=2)
print(f"Device '{document.get('name')}' configured for pod {document['user_id']}")
print(f"Gateway: {document['url']}")
PY
        ;;
    sync)
        python3 "$AGENT" --config "$POD_CONFIG" --data "$POD_DATA"
        ;;
    verify)
        python3 "$AGENT" --config "$POD_CONFIG" --data "$POD_DATA" --verify-only
        ;;
    status)
        echo "Device home: $DEVICE_HOME"
        if [[ -f "$POD_DATA/state.json" ]]; then
            echo "State: $(cat "$POD_DATA/state.json")"
        else
            echo "State: (never synced)"
        fi
        echo "Objects on disk: $(find "$POD_DATA/objects" -type f | wc -l)"
        echo "Bytes on disk:   $(du -sh "$POD_DATA/objects" 2>/dev/null | cut -f1)"
        echo
        find "$POD_DATA/objects" -type f | sort | head -20
        ;;
    *)
        echo "Usage: $0 {enrol '<string>'|sync|verify|status}" >&2
        exit 1
        ;;
esac
