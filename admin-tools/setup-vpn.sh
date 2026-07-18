#!/bin/bash
set -e

# Phase 1 operator runbook: joins a SmallWorlds node to its own private
# Headscale-based overlay network (Tailscale), points admin<env_ext>.<domain>
# at the node's tailnet IP for tailnet-only SSH/kubectl access, and prints
# instructions for enrolling the machine running this script.
#
# This is deliberately NOT part of cloud-init / bootstrap — joining needs a
# preauth key that only exists after ArgoCD has deployed Headscale, so it has
# to be a separate, deliberate step run after the cluster is already up.
#
# Phase 1 is Hetzner/production-only (see plans-and-walkthroughs/
# implementation_plan-private-overlay-phase0-1.md — local-target installs
# don't expose 22/6443 to the internet in the first place, so there's nothing
# for this to lock down there). This script assumes a Hetzner-provisioned
# node: root@<ip> SSH and the Hetzner DNS API for the admin.<domain> record.
#
# Prerequisites:
#   - The `headscale` ArgoCD Application must already be Synced + Healthy.
#   - HCLOUD_TOKEN available (env var, or cached in .smallworlds-cache.env).
#   - SSH access to the node as root.
#
# Usage:
#   ./admin-tools/setup-vpn.sh                 # production
#
# Does NOT touch the firewall — that is a separate, deliberate step
# (Step 1.4 in the plan doc) run only after tailnet access here is verified.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CACHE_FILE="$SCRIPT_DIR/../.smallworlds-cache.env"
[ -f "$CACHE_FILE" ] && source "$CACHE_FILE"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
DOMAIN=$(detect_domain)

if [ "${DEPLOY_TARGET:-hetzner}" = "local" ]; then
    echo "Warning: DEPLOY_TARGET is 'local'. Phase 1 is intended for Hetzner/production"
    echo "only — a local-target node doesn't expose 22/6443 to the internet by default,"
    echo "so there's normally nothing here to lock down. Re-check the plan doc before"
    echo "continuing if you didn't mean to run this against the local target."
    read -p "Continue anyway? (y/N): " confirm
    [[ "$confirm" =~ ^[Yy]$ ]] || exit 1
fi

export KUBECONFIG="${KUBECONFIG:-$(kubeconfig_path "$CLUSTER")}"
SERVER_IP=$(detect_server_ip "$CUR_ENV_EXT")
VPN_HOST="vpn${CUR_ENV_EXT}.${DOMAIN}"
ADMIN_HOST="admin${CUR_ENV_EXT}.${DOMAIN}"

echo "Setting up the private overlay network for the '$CLUSTER' cluster ($SERVER_IP)..."

echo ""
echo "1. Checking headscale is deployed and healthy..."
STATUS=$(kubectl get application headscale -n argocd -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null || true)
if [ "$STATUS" != "Synced/Healthy" ]; then
    echo "Error: headscale Application is not Synced/Healthy yet (status: ${STATUS:-not found})."
    echo "Wait for ArgoCD to finish deploying it, then re-run this script."
    exit 1
fi

echo ""
echo "2. Creating (or reusing) the 'infra' headscale user and a one-time preauth key..."
kubectl -n headscale exec deploy/headscale -- headscale users create infra >/dev/null 2>&1 || true
AUTH_KEY=$(kubectl -n headscale exec deploy/headscale -- headscale preauthkeys create --user infra --expiration 1h --reusable=false 2>/dev/null | tr -d '[:space:]')
if [ -z "$AUTH_KEY" ]; then
    echo "Error: could not create a headscale preauth key."
    exit 1
fi

echo ""
echo "3. Joining $SERVER_IP to the tailnet ($VPN_HOST)..."
ssh $SSH_OPTS "root@$SERVER_IP" "tailscale up --login-server=https://$VPN_HOST --auth-key=$AUTH_KEY"

TAILNET_IP=$(ssh $SSH_OPTS "root@$SERVER_IP" "tailscale ip -4" | tr -d '[:space:]')
if [ -z "$TAILNET_IP" ]; then
    echo "Error: node joined but did not report a tailnet IPv4 address."
    exit 1
fi
echo "   Node's tailnet IP: $TAILNET_IP"

echo ""
echo "4. Pointing $ADMIN_HOST at the tailnet IP..."
HCLOUD_TOKEN="${HCLOUD_TOKEN:?HCLOUD_TOKEN not set and not found in $CACHE_FILE}"
API="https://api.hetzner.cloud/v1"
AUTH="Authorization: Bearer $HCLOUD_TOKEN"
ZONE_ID=$(curl -sf -H "$AUTH" "$API/zones?name=${DOMAIN}" | python3 -c 'import json,sys; d=json.load(sys.stdin); zs=d.get("zones") or []; print(zs[0]["id"] if zs else "")')
if [ -z "$ZONE_ID" ]; then
    echo "Error: zone $DOMAIN not found in Hetzner DNS."
    exit 1
fi
# Same upsert pattern as the DDNS CronJob (bootstrap-local-node.sh): delete
# any stale record for this name, then create the current one.
ADMIN_NAME="admin${CUR_ENV_EXT}"
curl -sf -X DELETE -H "$AUTH" "$API/zones/$ZONE_ID/rrsets/$ADMIN_NAME/A" >/dev/null 2>&1 || true
curl -sf -X POST -H "$AUTH" -H "Content-Type: application/json" \
    -d "{\"name\":\"$ADMIN_NAME\",\"type\":\"A\",\"ttl\":300,\"records\":[{\"value\":\"$TAILNET_IP\"}]}" \
    "$API/zones/$ZONE_ID/rrsets" >/dev/null
echo "   $ADMIN_HOST -> $TAILNET_IP"

echo ""
echo "5. Adding tls-san for $ADMIN_HOST / $TAILNET_IP to the kube API cert..."
echo "   This briefly restarts k3s on $SERVER_IP (workloads keep running; the"
echo "   API server itself drops for a few seconds)."
read -p "   Continue? (y/N): " confirm
if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    echo "Skipped. Steps 1-4 already applied — re-run this script to finish."
    exit 0
fi
ssh $SSH_OPTS "root@$SERVER_IP" "cat > /etc/rancher/k3s/config.yaml" <<K3SCONF
tls-san:
  - "$ADMIN_HOST"
  - "$TAILNET_IP"
K3SCONF
ssh $SSH_OPTS "root@$SERVER_IP" "systemctl restart k3s"
echo "   Restarted. Waiting for the API server to come back..."
until ssh $SSH_OPTS "root@$SERVER_IP" "kubectl get nodes >/dev/null 2>&1"; do sleep 3; done
echo "   Back up."

echo ""
echo "✅ Server-side setup complete."
echo ""
echo "6. To enroll THIS machine (optional, for direct tailnet SSH/kubectl access):"
echo "     tailscale up --login-server=https://$VPN_HOST"
echo "   That prints a machine key. Approve it from the server:"
echo "     ssh root@$SERVER_IP 'kubectl -n headscale exec deploy/headscale -- headscale nodes register --user infra --key <machine-key>'"
echo ""
echo "Once you have verified tailnet SSH/kubectl access from an enrolled"
echo "device (ssh root@$ADMIN_HOST, kubectl against the tailnet-rewritten"
echo "kubeconfig), close 22/6443 in infrastructure/terraform/main.tf as a"
echo "separate, deliberate change — do not do it before verifying."
