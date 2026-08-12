#!/usr/bin/env bash
#
# Enrol a home device (or an app's export agent) against the pod gateway.
#
#   ./admin-tools/pod-enroll-device.sh --user <immich-user-id> --name alice-pi
#   ./admin-tools/pod-enroll-device.sh --agent immich
#
# Tokens are shown once and never stored: only their SHA-256 digests go into the
# pod-gateway-tokens Secret, so losing a token means minting a new one rather
# than recovering the old one.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/cluster-env.sh
source "${SCRIPT_DIR}/lib/cluster-env.sh"

ENV_NAME="production"
USER_ID=""
DEVICE_NAME=""
AGENT_SOURCE=""
GATEWAY_URL=""

usage() {
    cat <<'EOF'
Usage:
  pod-enroll-device.sh --user <user-id> --name <device-name> [options]
  pod-enroll-device.sh --agent <source>                      [options]

Options:
  --user <id>       Immich user id whose pod the device may read
  --name <name>     Human-readable device name (metrics label)
  --agent <source>  Mint an append-only agent token instead (e.g. "immich")
  --env <name>      Cluster environment (default: production)
  --url <url>       Gateway base URL (default: https://pod.<cluster domain>)
  -h, --help        Show this help
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user)  USER_ID="$2"; shift 2 ;;
        --name)  DEVICE_NAME="$2"; shift 2 ;;
        --agent) AGENT_SOURCE="$2"; shift 2 ;;
        --env)   ENV_NAME="$2"; shift 2 ;;
        --url)   GATEWAY_URL="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
    esac
done

if [[ -n "$AGENT_SOURCE" ]]; then
    [[ -z "$USER_ID$DEVICE_NAME" ]] || { echo "--agent cannot be combined with --user/--name" >&2; exit 1; }
elif [[ -z "$USER_ID" || -z "$DEVICE_NAME" ]]; then
    echo "Both --user and --name are required." >&2
    usage
    exit 1
fi

KUBECONFIG_PATH="$(kubeconfig_path "$ENV_NAME")"
if [[ ! -f "$KUBECONFIG_PATH" ]]; then
    echo "No kubeconfig at $KUBECONFIG_PATH" >&2
    exit 1
fi
export KUBECONFIG="$KUBECONFIG_PATH"

if [[ -z "$GATEWAY_URL" ]]; then
    host="$(kubectl get ingress pod-gateway -n pod-gateway \
        -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true)"
    if [[ -z "$host" ]]; then
        echo "Could not read the pod-gateway ingress; pass --url explicitly." >&2
        exit 1
    fi
    GATEWAY_URL="https://${host}"
fi

TOKEN="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
DIGEST="$(printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1)"

# Merge into the existing token document rather than replacing it, so enrolling
# a second device never revokes the first.
EXISTING='{}'
if kubectl get secret pod-gateway-tokens -n pod-gateway >/dev/null 2>&1; then
    EXISTING="$(kubectl get secret pod-gateway-tokens -n pod-gateway \
        -o jsonpath='{.data.tokens\.json}' | base64 -d)"
fi

WORKDIR="$(mktemp -d)"
chmod 700 "$WORKDIR"
trap 'rm -rf "$WORKDIR"' EXIT

EXISTING="$EXISTING" DIGEST="$DIGEST" USER_ID="$USER_ID" SOURCE="$AGENT_SOURCE" \
NAME="${DEVICE_NAME:-${AGENT_SOURCE}-exporter}" \
python3 -c '
import json, os, sys

document = json.loads(os.environ["EXISTING"] or "{}")
document.setdefault("agents", {})
document.setdefault("devices", {})

if os.environ["SOURCE"]:
    document["agents"][os.environ["DIGEST"]] = {
        "name": os.environ["NAME"],
        "sources": [os.environ["SOURCE"]],
    }
else:
    document["devices"][os.environ["DIGEST"]] = {
        "name": os.environ["NAME"],
        "user_id": os.environ["USER_ID"],
    }

with open(sys.argv[1], "w") as handle:
    json.dump(document, handle, indent=2, sort_keys=True)
' "$WORKDIR/tokens.json"

kubectl create secret generic pod-gateway-tokens \
    -n pod-gateway --from-file=tokens.json="$WORKDIR/tokens.json" \
    --dry-run=client -o yaml | kubectl apply -f -

if [[ -n "$AGENT_SOURCE" ]]; then
    # The exporter reads its own token from its tenant namespace.
    kubectl create secret generic "${AGENT_SOURCE}-pod-agent" \
        -n "$AGENT_SOURCE" --from-literal=token="$TOKEN" \
        --dry-run=client -o yaml | kubectl apply -f -
    echo
    echo "Agent token for '${AGENT_SOURCE}' minted and stored as"
    echo "  secret/${AGENT_SOURCE}-pod-agent in namespace ${AGENT_SOURCE}."
    echo "It may append to any pod and read none."
    exit 0
fi

# Tell the Immich exporter this user now has somewhere to export to.
CURRENT_USERS="$(kubectl get configmap immich-pod-users -n immich \
    -o jsonpath='{.data.users\.txt}' 2>/dev/null || true)"
printf '%s\n%s\n' "$CURRENT_USERS" "$USER_ID" \
    | grep -v '^$' | sort -u > "$WORKDIR/users.txt"
kubectl create configmap immich-pod-users \
    -n immich --from-file=users.txt="$WORKDIR/users.txt" \
    --dry-run=client -o yaml | kubectl apply -f -

ENROLLMENT="$(TOKEN="$TOKEN" USER_ID="$USER_ID" URL="$GATEWAY_URL" \
              NAME="$DEVICE_NAME" python3 -c '
import base64, json, os
print(base64.b64encode(json.dumps({
    "url": os.environ["URL"],
    "user_id": os.environ["USER_ID"],
    "token": os.environ["TOKEN"],
    "name": os.environ["NAME"],
}).encode()).decode())')"

cat <<EOF

Device '${DEVICE_NAME}' enrolled for user ${USER_ID}.

Give the owner this one-time enrolment string — it is not recoverable, and it
grants read access to their pod:

${ENROLLMENT}

They install it on the device with:

  sudo ./install.sh '${ENROLLMENT}'

from admin-tools/pod-device/ (see doc/pod-archive.md).
EOF
