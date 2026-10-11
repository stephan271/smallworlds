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
# Phase 1 covers every node that is directly on the internet: a Hetzner node,
# and a server rented elsewhere that was installed with the host firewall
# (docs/adr/0055). A LAN machine behind a router does not expose 22/6443 in the
# first place, so there is nothing for this to lock down there (see
# plans-and-walkthroughs/implementation_plan-private-overlay-phase0-1.md).
# It needs SSH to the node as root or as a user with passwordless sudo, and the
# Hetzner DNS API for the admin.<domain> record — the zone is at Hetzner
# wherever the server is.
#
# Prerequisites:
#   - The `headscale` ArgoCD Application must already be Synced + Healthy.
#   - HCLOUD_TOKEN available (env var, or cached in .smallworlds-cache.env).
#   - SSH access to the node as root, or as a user with passwordless sudo.
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
CACHED_DOMAIN="${DOMAIN:-}"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
DOMAIN=$(detect_domain)

RENTED=""
if [ "${DEPLOY_TARGET:-hetzner}" = "local" ] && [ "${LOCAL_FIREWALL:-no}" = "yes" ]; then
    RENTED="yes"
    # cluster-env.sh reads the Hetzner tfvars, which a rented server never had.
    # Its domain and kubeconfig are the ones the installer cached and wrote
    # (KUBECONFIG_LABEL in smallworlds-init.sh).
    DOMAIN="${CACHED_DOMAIN:?DOMAIN not found in $CACHE_FILE}"
    CLUSTER="local"
    [ -n "$CUR_ENV_EXT" ] && CLUSTER="local-${CUR_ENV_EXT#.}"
fi

if [ "${DEPLOY_TARGET:-hetzner}" = "local" ] && [ -z "$RENTED" ]; then
    echo "Warning: DEPLOY_TARGET is 'local'. Phase 1 is intended for Hetzner/production"
    echo "only — a local-target node doesn't expose 22/6443 to the internet by default,"
    echo "so there's normally nothing here to lock down. Re-check the plan doc before"
    echo "continuing if you didn't mean to run this against the local target."
    read -p "Continue anyway? (y/N): " confirm
    [[ "$confirm" =~ ^[Yy]$ ]] || exit 1
fi

export KUBECONFIG="${KUBECONFIG:-$(kubeconfig_path "$CLUSTER")}"
if [ -n "$RENTED" ]; then
    # A rented server is reached the way the installer reached it: the same
    # login, which may be root or a user with sudo, and through the same
    # ~/.ssh/config entry if it used one (a non-default port, for instance).
    NODE_SSH="${LOCAL_SSH_TARGET:?LOCAL_SSH_TARGET not found in $CACHE_FILE}"
    SERVER_IP="${NODE_SSH#*@}"
else
    SERVER_IP=$(detect_server_ip "$CUR_ENV_EXT")
    NODE_SSH="root@$SERVER_IP"
fi

# Everything on the node runs as root. A login that is not root goes through
# sudo, which has to work without a password: there is no terminal on these
# connections to type one into.
NODE_USER="${NODE_SSH%%@*}"
[ "$NODE_USER" = "$NODE_SSH" ] && NODE_USER="$(id -un)"
AS_ROOT=""        # prefix for commands this script runs
SUDO_HINT=""      # the same, for commands it prints for the operator
if [ "$NODE_USER" != "root" ]; then AS_ROOT="sudo -n "; SUDO_HINT="sudo "; fi

# node_run <shell command>: run it on the node as root. The command is one
# single-quoted word on the far side, so pipes and redirections in it happen
# there, as root, and not in the login shell.
node_run() {
    local quoted
    quoted="'$(printf '%s' "$1" | sed "s/'/'\\\\''/g")'"
    ssh $SSH_OPTS "$NODE_SSH" "${AS_ROOT}sh -c $quoted"
}

if ! node_run true 2>/dev/null; then
    echo "Error: cannot run commands as root on the node through '$NODE_SSH'."
    if [ -n "$AS_ROOT" ]; then
        echo "'$NODE_USER' needs sudo without a password for this (check with: ssh $NODE_SSH sudo -n true)."
    else
        echo "Check that 'ssh $NODE_SSH' works with your key."
    fi
    exit 1
fi

# What to type to reach the node by another name: ssh may be on a port that
# only ~/.ssh/config knows for the name the installer used.
NODE_PORT="$(ssh -G "$NODE_SSH" 2>/dev/null | awk '$1 == "port" { print $2 }')"
PORT_FLAG=""
if [ -n "$NODE_PORT" ] && [ "$NODE_PORT" != "22" ]; then PORT_FLAG="-p $NODE_PORT "; fi

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
# Current Headscale (seen with 0.29.2) identifies the user of a preauth key by
# its numeric id, and rejects the name ("invalid argument \"infra\" for --user"). Older
# releases take the name, which is the fallback when no id can be read.
INFRA_USER=$(kubectl -n headscale exec deploy/headscale -- headscale users list -o json 2>/dev/null \
    | python3 -c 'import json, sys
try:
    users = json.load(sys.stdin)
except ValueError:
    users = []
print(next((str(u["id"]) for u in users or [] if u.get("name") == "infra"), ""))' 2>/dev/null || true)
[ -n "$INFRA_USER" ] || INFRA_USER="infra"
KEY_ERROR=$(mktemp)
AUTH_KEY=$(kubectl -n headscale exec deploy/headscale -- headscale preauthkeys create --user "$INFRA_USER" --expiration 1h --reusable=false 2>"$KEY_ERROR" | tr -d '[:space:]')
if [ -z "$AUTH_KEY" ]; then
    echo "Error: could not create a headscale preauth key. Headscale said:"
    sed 's/^/    /' "$KEY_ERROR"
    rm -f "$KEY_ERROR"
    exit 1
fi
rm -f "$KEY_ERROR"

if ! node_run "command -v tailscale >/dev/null 2>&1"; then
    # The Hetzner cloud-init installs the client; the local bootstrap runs only
    # what came in its verified archive, so on a rented server it is missing
    # until someone decides to fetch it. That decision is made here, in view.
    echo ""
    echo "   The Tailscale client is not installed on $SERVER_IP. It is installed with"
    echo "   Tailscale's own script (https://tailscale.com/install.sh), run as root."
    read -p "   Install it now? (y/N): " confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        echo "Stopped. Install the tailscale package on the node and re-run this script."
        exit 1
    fi
    node_run "curl -fsSL https://tailscale.com/install.sh | sh"
fi

echo ""
echo "3. Joining $SERVER_IP to the tailnet ($VPN_HOST)..."
node_run "tailscale up --login-server=https://$VPN_HOST --auth-key=$AUTH_KEY"

TAILNET_IP=$(node_run "tailscale ip -4" | tr -d '[:space:]')
if [ -z "$TAILNET_IP" ]; then
    echo "Error: node joined but did not report a tailnet IPv4 address."
    exit 1
fi
echo "   Node's tailnet IP: $TAILNET_IP"

if [ "${LOCAL_DNS:-hetzner}" = "manual" ] && [ -n "$RENTED" ]; then
    # DNS is kept by hand for this cluster (docs/adr/0056), and a wildcard
    # record would send admin.<domain> to the public address — exactly where
    # SSH is about to stop answering. So this one record has to be explicit.
    echo ""
    echo "4. Create this DNS record yourself, at your DNS provider:"
    echo "       $ADMIN_HOST   A   $TAILNET_IP"
    echo "   It must be an explicit record: your wildcard would otherwise answer"
    echo "   for it with the public address."
    read -p "   Press Enter once it is created (it may take a while to be visible)... " _
else
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
fi

echo ""
echo "5. Adding tls-san for $ADMIN_HOST / $TAILNET_IP to the kube API cert..."
echo "   This briefly restarts k3s on $SERVER_IP (workloads keep running; the"
echo "   API server itself drops for a few seconds)."
read -p "   Continue? (y/N): " confirm
if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    echo "Skipped. Steps 1-4 already applied — re-run this script to finish."
    exit 0
fi
node_run "cat > /etc/rancher/k3s/config.yaml" <<K3SCONF
tls-san:
  - "$ADMIN_HOST"
  - "$TAILNET_IP"
K3SCONF
node_run "systemctl restart k3s"
echo "   Restarted. Waiting for the API server to come back..."
until node_run "k3s kubectl get nodes >/dev/null 2>&1"; do sleep 3; done
echo "   Back up."

echo ""
echo "✅ Server-side setup complete."
echo ""
echo "6. To enroll THIS machine (optional, for direct tailnet SSH/kubectl access):"
echo "     tailscale up --login-server=https://$VPN_HOST"
echo "   That prints a link ending in an id. Approve it from the server:"
echo "     ssh $NODE_SSH '${SUDO_HINT}k3s kubectl -n headscale exec deploy/headscale -- headscale auth register --auth-id <id> --user infra'"
echo "   (the page behind the link shows the same command with the id filled in;"
echo "   older Headscale releases use: headscale nodes register --user infra --key <id>)"
echo ""
echo "7. Verify tailnet access from the enrolled device BEFORE closing anything:"
echo "     ssh ${PORT_FLAG}${NODE_USER}@$ADMIN_HOST"
echo "     kubectl get nodes      # against the tailnet-rewritten kubeconfig"
echo "   (step 5 added $ADMIN_HOST to the API server certificate, so the"
echo "   kubeconfig must name that host rather than the public IP.)"
echo ""
echo "8. Only once BOTH work, close the public administrative ports:"
if [ -n "$RENTED" ]; then
    if [ -n "$SUDO_HINT" ]; then
        # sudo drops SSH_CONNECTION, which is how the tool sees where the
        # session came from; without it a tailnet login reads as a local one.
        echo "     ssh ${PORT_FLAG}${NODE_USER}@$ADMIN_HOST sudo --preserve-env=SSH_CONNECTION smallworlds-firewall close"
    else
        echo "     ssh ${PORT_FLAG}root@$ADMIN_HOST smallworlds-firewall close"
    fi
    echo "   Run it over the tailnet, as written: the tool refuses unless the session"
    echo "   it is called from came in that way, because that session is the proof"
    echo "   the way in still works. SSH and kubectl then answer on the tailnet only."
    echo ""
    echo "   Way back in after a lockout: your provider's web console, and there"
    echo "     smallworlds-firewall open"
    echo "   Re-running the installer does not reopen the ports."
else
    echo "     terraform apply -var 'public_admin_ports=false'"
    echo "   SSH and kubectl keep working over the tailnet: that traffic arrives"
    echo "   inside the WireGuard tunnel and never passes the Hetzner firewall."
    echo "   Do not do this before step 7 — it is how you lock yourself out."
    echo ""
    echo "   Way back in after a lockout: re-apply with public_admin_ports=true,"
    echo "   or edit the firewall directly in the Hetzner Cloud console, which"
    echo "   needs no access to the server at all."
fi
