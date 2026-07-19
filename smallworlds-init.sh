#!/usr/bin/env bash
set -e

# Colors for pretty output
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Shared helpers (cluster_label, kubeconfig_path)
source "$(cd "$(dirname "$0")" && pwd)/admin-tools/lib/cluster-env.sh"

echo -e "${CYAN}======================================================${NC}"
echo -e "${CYAN}        Welcome to the SmallWorlds Installer         ${NC}"
echo -e "${CYAN}======================================================${NC}"
echo ""



echo -e "This wizard will spin up your fully automated sovereign cloud."
echo ""

CACHE_FILE=".smallworlds-cache.env"
if [[ -f "$CACHE_FILE" ]]; then
    source "$CACHE_FILE"
fi

ask_with_default() {
    local prompt_text="$1"
    local var_name="$2"
    local is_secret="$3"
    
    local current_val="${!var_name}"

    if [[ -n "$current_val" ]]; then
        if [[ "$is_secret" == "true" ]]; then
            read -s -p "$prompt_text [saved]: " input_val
            echo ""
        else
            read -p "$prompt_text [$current_val] (enter - to clear): " input_val
        fi

        if [[ -z "$input_val" ]]; then
            declare -g "$var_name"="$current_val"
        elif [[ "$input_val" == "-" ]]; then
            declare -g "$var_name"=""
        else
            declare -g "$var_name"="$input_val"
        fi
    else
        if [[ "$is_secret" == "true" ]]; then
            read -s -p "$prompt_text: " input_val
            echo ""
        else
            read -p "$prompt_text: " input_val
        fi
        declare -g "$var_name"="$input_val"
    fi
}

# Ensure the DNS zone for $DOMAIN exists in Hetzner DNS (uses $HCLOUD_TOKEN).
# Used by the hetzner target and by internet-exposed local deployments.
ensure_dns_zone() {
    echo -e "${CYAN}Ensuring DNS Zone '$DOMAIN' exists in Hetzner...${NC}"
    local zone_exists api_response
    zone_exists=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/zones" | grep -o "\"name\":\"$DOMAIN\"" || true)

    if [ -z "$zone_exists" ]; then
        echo -e "${YELLOW}Zone $DOMAIN not found. Creating it...${NC}"
        api_response=$(curl -s -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/zones" -d '{"name":"'"$DOMAIN"'", "mode": "primary", "ttl": 3600}')
        if echo "$api_response" | grep -q "\"error\""; then
            echo -e "${RED}Failed to create zone: $(echo "$api_response" | jq -r '.error.message' || echo "$api_response")${NC}"
        else
            echo -e "${GREEN}Zone created successfully.${NC}"
        fi
    else
        echo -e "${GREEN}Zone $DOMAIN already exists.${NC}"
    fi
}

# Ensure the shared "SmallWorlds Admin Key" exists in Hetzner Cloud (uses
# $HCLOUD_TOKEN) and set $SSH_KEY_ID to its id. Idempotent, same pattern as
# ensure_dns_zone: whichever deployment (dev or prod, either order) runs
# first uploads it; later ones just find and reuse it. Terraform itself
# never creates/owns this key — see infrastructure/terraform/main.tf locals.
ensure_ssh_key() {
    local pubkey_path="${SSH_PUBLIC_KEY_PATH:-$HOME/.ssh/id_ed25519.pub}"
    if [[ ! -f "$pubkey_path" ]]; then
        echo -e "${RED}SSH public key not found at $pubkey_path — generate one with 'ssh-keygen -t ed25519' first.${NC}"
        exit 1
    fi

    echo -e "${CYAN}Ensuring Hetzner SSH key 'SmallWorlds Admin Key' exists...${NC}"
    SSH_KEY_ID=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/ssh_keys?name=SmallWorlds%20Admin%20Key" | grep -o '"id":[0-9]*' | head -1 | grep -o '[0-9]*')

    if [ -z "$SSH_KEY_ID" ]; then
        echo -e "${YELLOW}Key not found. Uploading $pubkey_path...${NC}"
        local pubkey_json api_response
        pubkey_json=$(python3 -c 'import json,sys; print(json.dumps(open(sys.argv[1]).read().strip()))' "$pubkey_path")
        api_response=$(curl -s -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/ssh_keys" -d "{\"name\":\"SmallWorlds Admin Key\", \"public_key\": $pubkey_json}")
        SSH_KEY_ID=$(echo "$api_response" | grep -o '"id":[0-9]*' | head -1 | grep -o '[0-9]*')
        if [ -z "$SSH_KEY_ID" ]; then
            echo -e "${RED}Failed to upload SSH key: $(echo "$api_response" | jq -r '.error.message' 2>/dev/null || echo "$api_response")${NC}"
            exit 1
        fi
        echo -e "${GREEN}SSH key uploaded (id: $SSH_KEY_ID).${NC}"
    else
        echo -e "${GREEN}SSH key already exists in Hetzner (id: $SSH_KEY_ID).${NC}"
    fi
}

echo -e "${YELLOW}Gathering Configuration...${NC}"
echo -e "Deployment targets:"
echo -e "  ${CYAN}hetzner${NC} — provision a Hetzner Cloud VM + DNS via Terraform (public, internet-facing)"
echo -e "  ${CYAN}local${NC}   — bootstrap an existing Linux machine in your LAN (e.g. a laptop/mini-PC)"
if [[ -z "$DEPLOY_TARGET" ]]; then
    DEPLOY_TARGET="hetzner"
fi
ask_with_default "1. Select deployment target (hetzner or local)" "DEPLOY_TARGET" "false"
if [[ "$DEPLOY_TARGET" != "hetzner" && "$DEPLOY_TARGET" != "local" ]]; then
    echo -e "${YELLOW}Unknown target '$DEPLOY_TARGET' — must be 'hetzner' or 'local'.${NC}"
    exit 1
fi

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo -e "${YELLOW}Note: You must manually register your domain with a registrar of your choice.${NC}"
    echo -e "${YELLOW}This script will only configure the DNS zone in Hetzner Cloud (which is free).${NC}"
    echo -e "${YELLOW}Domain registration itself is not automated and will incur costs at your registrar.${NC}"
else
    echo -e "${YELLOW}Note: For a LAN-only deployment the domain does not need to be registered anywhere —${NC}"
    echo -e "${YELLOW}name resolution happens inside your LAN (router DNS, Pi-hole, or /etc/hosts entries).${NC}"
    echo -e "${YELLOW}If you choose internet exposure later in this wizard, the domain MUST be registered,${NC}"
    echo -e "${YELLOW}with its nameservers pointed at Hetzner DNS (helium/oxygen/hydrogen.ns.hetzner.*).${NC}"
fi
ask_with_default "2. Enter your target domain (e.g. smallworlds.network)" "DOMAIN" "false"
ask_with_default "3. Enter the admin email address" "ADMIN_EMAIL" "false"

# Ensure ONBOARDING_MODE has a valid default if empty
if [[ -z "$ONBOARDING_MODE" ]]; then
    ONBOARDING_MODE="invitation"
fi
ask_with_default "4. Select onboarding mode (invitation or self-registration)" "ONBOARDING_MODE" "false"

ask_with_default "5. Enter environment extension (e.g. .dev, or leave empty for prod)" "ENV_EXT" "false"

echo ""
echo -e "${YELLOW}GitOps Repository Configuration${NC}"
ask_with_default "6. Enter your Git repository URL (e.g., https://github.com/my-community/config.git)" "GITOPS_REPO_URL" "false"
ask_with_default "7. Enter your Git username" "GITOPS_REPO_USER" "false"
ask_with_default "8. Paste your Git Access Token" "GITOPS_REPO_TOKEN" "true"

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo ""
    echo -e "${YELLOW}Hetzner Configuration${NC}"
    ask_with_default "9. Paste your Hetzner Cloud API Token" "HCLOUD_TOKEN" "true"
else
    echo ""
    echo -e "${YELLOW}Local Server Configuration${NC}"
    echo -e "The target machine needs: a systemd-based Linux, SSH access, and sudo/root rights."
    echo -e "Use ${CYAN}user@host${NC} (e.g. root@192.168.1.50), or ${CYAN}localhost${NC} to install on THIS machine."
    ask_with_default "9. Enter the SSH target of your local server" "LOCAL_SSH_TARGET" "false"
    if [[ -z "$DATA_DIR" ]]; then
        DATA_DIR="/var/lib/smallworlds-data"
    fi
    ask_with_default "10. Enter the data directory on the server (all user data lives here)" "DATA_DIR" "false"

    echo ""
    echo -e "Optionally, the apps can be exposed on the internet: DNS records for your domain"
    echo -e "are then managed in Hetzner DNS (free, token required) and kept pointed at your"
    echo -e "connection's public IP by an in-cluster DDNS job, and certificates come from"
    echo -e "Let's Encrypt instead of being self-signed. Requires: a registered domain with"
    echo -e "nameservers at Hetzner DNS, a public IPv4 (no CGNAT), and these forwards on your"
    echo -e "router to the server: ${CYAN}80/tcp, 443/tcp, 10000/udp${NC}."
    if [[ -z "$LOCAL_PUBLIC" ]]; then
        LOCAL_PUBLIC="no"
    fi
    ask_with_default "11. Expose the apps on the internet? (yes/no)" "LOCAL_PUBLIC" "false"
    case "$LOCAL_PUBLIC" in
        y|Y|yes|Yes|YES) LOCAL_PUBLIC="yes";;
        *) LOCAL_PUBLIC="no";;
    esac
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        ask_with_default "12. Paste your Hetzner API Token (used for DNS record management only)" "HCLOUD_TOKEN" "true"
    fi
fi

# Auto-convert SSH URLs to HTTPS if access token is used
if [[ -n "$GITOPS_REPO_TOKEN" ]]; then
    if [[ "$GITOPS_REPO_URL" =~ ^git@([^:]+):(.+)$ ]]; then
        echo -e "${YELLOW}Auto-converting SSH Git URL to HTTPS for PAT authentication...${NC}"
        GITOPS_REPO_URL="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
    elif [[ "$GITOPS_REPO_URL" =~ ^ssh://git@([^/]+)/(.+)$ ]]; then
        echo -e "${YELLOW}Auto-converting SSH Git URL to HTTPS for PAT authentication...${NC}"
        GITOPS_REPO_URL="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
    fi
fi

# Generate passwords if not cached
if [[ -z "$KC_PASS" ]]; then KC_PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi
if [[ -z "$INVITE_SECRET" ]]; then INVITE_SECRET=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi
if [[ -z "$GARAGE_RPC_SECRET" ]]; then GARAGE_RPC_SECRET=$(openssl rand -hex 32); fi
if [[ -z "$GARAGE_ADMIN_TOKEN" ]]; then GARAGE_ADMIN_TOKEN=$(openssl rand -hex 32); fi
if [[ -z "$GRAFANA_PASS" ]]; then GRAFANA_PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi

# Save values to cache for next time
cat <<EOF > "$CACHE_FILE"
DEPLOY_TARGET="${DEPLOY_TARGET}"
LOCAL_SSH_TARGET="${LOCAL_SSH_TARGET}"
DATA_DIR="${DATA_DIR}"
LOCAL_PUBLIC="${LOCAL_PUBLIC}"
DOMAIN="${DOMAIN}"
ENV_EXT="${ENV_EXT}"
ADMIN_EMAIL="${ADMIN_EMAIL}"
ONBOARDING_MODE="${ONBOARDING_MODE}"
HCLOUD_TOKEN="${HCLOUD_TOKEN}"
GITOPS_REPO_URL="${GITOPS_REPO_URL}"
GITOPS_REPO_USER="${GITOPS_REPO_USER}"
GITOPS_REPO_TOKEN="${GITOPS_REPO_TOKEN}"
KC_PASS="${KC_PASS}"
INVITE_SECRET="${INVITE_SECRET}"
GARAGE_RPC_SECRET="${GARAGE_RPC_SECRET}"
GARAGE_ADMIN_TOKEN="${GARAGE_ADMIN_TOKEN}"
GRAFANA_PASS="${GRAFANA_PASS}"
EOF
chmod 600 "$CACHE_FILE"

echo ""

echo -e "${CYAN}Generating configuration...${NC}"

# Update ONBOARDING_MODE in the job manifest
sed -i -E "s/value: \"(invitation|self-registration)\"/value: \"$ONBOARDING_MODE\"/g" infrastructure/kubernetes/tenants/keycloak/realm-config-job.yaml

# Token that lands in cluster secrets (stalwart-dns-secrets): a LAN-only
# local deployment must not carry it into the cluster (no DNS automation
# there) — but never blank HCLOUD_TOKEN itself, it stays cached for future
# hetzner or internet-exposed runs.
SECRETS_HCLOUD_TOKEN="$HCLOUD_TOKEN"
if [[ "$DEPLOY_TARGET" == "local" && "$LOCAL_PUBLIC" != "yes" ]]; then
    SECRETS_HCLOUD_TOKEN=""
fi

# Generate the secrets manifest — deployed into the node's k3s auto-apply
# manifests directory on both deployment targets
SECRETS_FILE="/tmp/smallworlds-${DOMAIN}-secrets.yaml"
cat <<EOF > "$SECRETS_FILE"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: smallworlds-global-config
  namespace: default
data:
  ADMIN_EMAIL: "${ADMIN_EMAIL}"
  DOMAIN: "${DOMAIN}"
  ENV_EXT: "${ENV_EXT}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
---
apiVersion: v1
kind: Secret
metadata:
  name: keycloak-admin-creds
  namespace: keycloak
type: Opaque
stringData:
  admin-password: "${KC_PASS}"
  bulk-invite-secret: "${INVITE_SECRET}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: stalwart
---
apiVersion: v1
kind: Secret
metadata:
  name: stalwart-dns-secrets
  namespace: stalwart
type: Opaque
stringData:
  HCLOUD_TOKEN: "${SECRETS_HCLOUD_TOKEN}"
  DOMAIN: "${DOMAIN}"
  ENV_EXT: "${ENV_EXT}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager
---
apiVersion: v1
kind: Secret
metadata:
  name: hetzner
  namespace: cert-manager
type: Opaque
stringData:
  token: "${SECRETS_HCLOUD_TOKEN}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: argocd
---
apiVersion: v1
kind: Secret
metadata:
  name: repo-git-creds
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
stringData:
  url: "${GITOPS_REPO_URL}"
  username: "${GITOPS_REPO_USER}"
  password: "${GITOPS_REPO_TOKEN}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
---
apiVersion: v1
kind: Secret
metadata:
  name: grafana-admin-creds
  namespace: monitoring
type: Opaque
stringData:
  admin-user: "admin"
  admin-password: "${GRAFANA_PASS}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: garage-system
---
apiVersion: v1
kind: Secret
metadata:
  name: garage-auth-secret
  namespace: garage-system
type: Opaque
stringData:
  rpcSecret: "${GARAGE_RPC_SECRET}"
  adminToken: "${GARAGE_ADMIN_TOKEN}"
EOF
chmod 600 "$SECRETS_FILE"

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    # ==================================================================
    # Target: Hetzner Cloud — Terraform provisions VM + DNS, cloud-init
    # bootstraps k3s/ArgoCD on first boot.
    # ==================================================================

    # Export Hetzner Token as environment variable so Terraform can find it
    export HCLOUD_TOKEN=$HCLOUD_TOKEN

    # Ensure DNS Zone exists in Hetzner Cloud DNS
    ensure_dns_zone

    # Ensure the shared admin SSH key exists (sets $SSH_KEY_ID)
    ensure_ssh_key

    # Boot from the golden image (preloaded k3s + container images) if one exists
    GOLDEN_COUNT=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/images?type=snapshot&label_selector=smallworlds-golden%3Dtrue" \
        | grep -c '"id"' || true)
    if [ "$GOLDEN_COUNT" -gt 0 ]; then
        echo -e "${GREEN}Golden image found — fast boot enabled (skips updates, k3s download and image pulls).${NC}"
        export TF_VAR_use_golden_image=true
    else
        echo -e "${YELLOW}No golden image found — booting plain Ubuntu (build one with admin-tools/build-golden-image.sh).${NC}"
    fi

    # Set Terraform Git variables
    TF_GIT_USER="${GITOPS_REPO_USER}"
    TF_GIT_TOKEN="${GITOPS_REPO_TOKEN}"

    # 2. Generate temporary tfvars file
    TFVARS_FILE="/tmp/smallworlds-${DOMAIN}.tfvars"
    cat <<EOF > "$TFVARS_FILE"
domain_name       = "${DOMAIN}"
env_ext           = "${ENV_EXT}"
git_url        = "${GITOPS_REPO_URL}"
git_username   = "${TF_GIT_USER}"
git_password   = "${TF_GIT_TOKEN}"
hcloud_token      = "${HCLOUD_TOKEN}"
ssh_key_id        = ${SSH_KEY_ID}

EOF

    # 3. Execute Terraform
    echo -e "${CYAN}Initializing infrastructure... This will take a few minutes.${NC}"
    cd infrastructure/terraform

    terraform init -input=false > /dev/null
    terraform apply -var-file="$TFVARS_FILE" -auto-approve

    # 4. Capture Outputs
    SERVER_IP=$(terraform output -raw server_ipv4)
    cd ../..
    rm -f "$TFVARS_FILE"

    # 5. Retrieve Kubeconfig
    echo -e "${CYAN}Waiting for SSH to be available on $SERVER_IP...${NC}"
    while ! timeout 2 bash -c "</dev/tcp/$SERVER_IP/22" 2>/dev/null; do
        sleep 2
    done

    echo -e "${CYAN}Deploying secrets to the server securely...${NC}"
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@"$SERVER_IP" "mkdir -p /var/lib/rancher/k3s/server/manifests" 2>/dev/null
    scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$SECRETS_FILE" root@"$SERVER_IP":/var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml >/dev/null 2>&1
    rm -f "$SECRETS_FILE"


    echo -e "${CYAN}Waiting for K3s to generate kubeconfig on the remote node...${NC}"
    until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 root@"$SERVER_IP" "[ -f /etc/rancher/k3s/k3s.yaml ]" 2>/dev/null; do
        sleep 2
    done

    KUBECONFIG_LOCAL="$(kubeconfig_path "$(cluster_label "$ENV_EXT")")"
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@"$SERVER_IP" "cat /etc/rancher/k3s/k3s.yaml" > "$KUBECONFIG_LOCAL" 2>/dev/null
    sed -i "s|127.0.0.1|$SERVER_IP|g" "$KUBECONFIG_LOCAL"
    chmod 600 "$KUBECONFIG_LOCAL"

    echo -e "${CYAN}Retrieving ArgoCD initial admin password...${NC}"
    until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 root@"$SERVER_IP" "kubectl -n argocd get secret argocd-initial-admin-secret >/dev/null 2>&1" 2>/dev/null; do
        sleep 2
    done
    ARGOCD_PASS=$(ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@"$SERVER_IP" "kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath=\"{.data.password}\" | base64 -d" 2>/dev/null)
else
    # ==================================================================
    # Target: local server — an existing Linux machine in your LAN is
    # bootstrapped in place by infrastructure/local/bootstrap-local-node.sh
    # (the shell counterpart of the cloud-init template). No Terraform,
    # no public DNS, self-signed certificates.
    # ==================================================================
    REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
    BOOTSTRAP_SRC="$REPO_ROOT/infrastructure/local/bootstrap-local-node.sh"

    # Internet exposure: public DNS in Hetzner (maintained by an in-cluster
    # DDNS CronJob, since home IPs change) and Let's Encrypt certificates.
    # LAN-only deployments keep ACME_EMAIL empty -> self-signed issuer.
    LOCAL_ACME_EMAIL=""
    MANAGE_DNS=""
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        LOCAL_ACME_EMAIL="$ADMIN_EMAIL"
        MANAGE_DNS="true"

        ensure_dns_zone

        PUBLIC_IP=$(curl -4 -sf --max-time 10 https://api.ipify.org || true)
        echo -e "${CYAN}Detected public IP: ${PUBLIC_IP:-unknown}${NC}"
        echo -e "${YELLOW}Reminder: forward 80/tcp, 443/tcp and 10000/udp on your router to the server,${NC}"
        echo -e "${YELLOW}or certificate issuance and app access from the internet will fail.${NC}"

        # The DDNS CronJob (created by the bootstrap) reads the token from
        # this secret; the A records appear within its first run (~5 min).
        cat <<EOF >> "$SECRETS_FILE"
---
apiVersion: v1
kind: Namespace
metadata:
  name: ddns
---
apiVersion: v1
kind: Secret
metadata:
  name: hetzner-dns-token
  namespace: ddns
type: Opaque
stringData:
  HCLOUD_TOKEN: "${HCLOUD_TOKEN}"
EOF
    fi

    # Config consumed by the bootstrap script on the target machine.
    LOCAL_ENV_FILE="/tmp/smallworlds-${DOMAIN}-local.env"
    cat <<EOF > "$LOCAL_ENV_FILE"
DOMAIN="${DOMAIN}"
ENV_EXT="${ENV_EXT}"
ROOT_APP_GIT_URL="${GITOPS_REPO_URL}"
ACME_EMAIL="${LOCAL_ACME_EMAIL}"
MANAGE_DNS="${MANAGE_DNS}"
DATA_DIR="${DATA_DIR}"
NODE_NAME="smallworlds-local-node"
SECRETS_MANIFEST="/tmp/smallworlds-${DOMAIN}-secrets.yaml"
EOF
    chmod 600 "$LOCAL_ENV_FILE"

    REMOTE_KUBECONFIG="/tmp/smallworlds-kubeconfig.yaml"
    if [[ "$LOCAL_SSH_TARGET" == "localhost" ]]; then
        echo -e "${CYAN}Bootstrapping THIS machine (sudo password may be requested)...${NC}"
        sudo bash "$BOOTSTRAP_SRC" "$LOCAL_ENV_FILE"
        KUBECONFIG_FETCH() { cp "$REMOTE_KUBECONFIG" "$1" && rm -f "$REMOTE_KUBECONFIG"; }
    else
        # root logs in directly; any other user gets a sudo prefix
        SUDO_PREFIX="sudo "
        if [[ "$LOCAL_SSH_TARGET" == root@* ]]; then SUDO_PREFIX=""; fi

        echo -e "${CYAN}Copying bootstrap files to ${LOCAL_SSH_TARGET}...${NC}"
        scp $SSH_OPTS "$BOOTSTRAP_SRC" "$LOCAL_SSH_TARGET:/tmp/smallworlds-bootstrap-node.sh" >/dev/null
        scp $SSH_OPTS "$LOCAL_ENV_FILE" "$LOCAL_SSH_TARGET:$LOCAL_ENV_FILE" >/dev/null
        scp $SSH_OPTS "$SECRETS_FILE" "$LOCAL_SSH_TARGET:$SECRETS_FILE" >/dev/null

        echo -e "${CYAN}Bootstrapping ${LOCAL_SSH_TARGET} (sudo password may be requested)...${NC}"
        # -t: allocate a TTY so sudo can prompt for a password if needed
        ssh -t $SSH_OPTS "$LOCAL_SSH_TARGET" "${SUDO_PREFIX}bash /tmp/smallworlds-bootstrap-node.sh $LOCAL_ENV_FILE"
        KUBECONFIG_FETCH() {
            scp $SSH_OPTS "$LOCAL_SSH_TARGET:$REMOTE_KUBECONFIG" "$1" >/dev/null
            ssh $SSH_OPTS "$LOCAL_SSH_TARGET" "rm -f $REMOTE_KUBECONFIG /tmp/smallworlds-bootstrap-node.sh" 2>/dev/null || true
        }
    fi
    rm -f "$SECRETS_FILE" "$LOCAL_ENV_FILE"

    # Local clusters get their own kubeconfig label so they never clobber a
    # real production/dev kubeconfig from a Hetzner deployment.
    KUBECONFIG_LABEL="local"
    [[ -n "$ENV_EXT" ]] && KUBECONFIG_LABEL="local-${ENV_EXT#.}"
    KUBECONFIG_LOCAL="$(kubeconfig_path "$KUBECONFIG_LABEL")"
    KUBECONFIG_FETCH "$KUBECONFIG_LOCAL"
    chmod 600 "$KUBECONFIG_LOCAL"

    # The bootstrap rewrote 127.0.0.1 to the node's LAN IP — read it back
    SERVER_IP=$(sed -n 's|.*server: https://\([^:]*\):6443.*|\1|p' "$KUBECONFIG_LOCAL" | head -1)

    echo -e "${CYAN}Retrieving ArgoCD initial admin password...${NC}"
    ARGOCD_PASS="(retrieve with: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
    if command -v kubectl >/dev/null 2>&1; then
        for i in $(seq 1 60); do
            if KUBECONFIG="$KUBECONFIG_LOCAL" kubectl -n argocd get secret argocd-initial-admin-secret >/dev/null 2>&1; then
                ARGOCD_PASS=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
                break
            fi
            sleep 2
        done
    fi

    # LAN name resolution: nothing manages DNS for a local deployment, so
    # hand the operator a ready-made hosts block for their router/Pi-hole
    # or the /etc/hosts of each device.
    mkdir -p "$HOME/.smallworlds"
    HOSTS_SNIPPET="$HOME/.smallworlds/hosts-${KUBECONFIG_LABEL}.txt"
    {
        printf '%s' "$SERVER_IP"
        for sub in identity dashboard files photos git mail webmail monitoring whiteboard meet office plan; do
            printf ' %s%s.%s' "$sub" "$ENV_EXT" "$DOMAIN"
        done
        printf '\n'
    } > "$HOSTS_SNIPPET"
fi

echo ""
echo -e "${GREEN}======================================================${NC}"
echo -e "${GREEN}             Deployment Successful!                   ${NC}"
echo -e "${GREEN}======================================================${NC}"
echo ""
if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo -e "Your applications will take a few minutes to boot up and fetch their SSL certificates."
elif [[ "$LOCAL_PUBLIC" == "yes" ]]; then
    echo -e "Your applications will take a few minutes to boot up. The in-cluster DDNS job creates"
    echo -e "the public DNS records on its first run (within ~5 minutes); Let's Encrypt certificates"
    echo -e "are issued shortly after — expect certificate warnings until then."
    echo ""
    echo -e "${YELLOW}Notes for internet-exposed local deployments:${NC}"
    echo -e " - Router forwards required: ${CYAN}80/tcp, 443/tcp, 10000/udp${NC} -> ${CYAN}${SERVER_IP}${NC}."
    echo -e " - If devices INSIDE your LAN cannot reach the apps, your router does not"
    echo -e "   support hairpin NAT — add this line to your router DNS/Pi-hole//etc/hosts"
    echo -e "   (saved to ${CYAN}${HOSTS_SNIPPET}${NC}):"
    echo ""
    cat "$HOSTS_SNIPPET"
    echo ""
    echo -e " - Stalwart mail deploys and its DNS records are automated, but real mail"
    echo -e "   delivery from a home connection is unreliable (ISPs block port 25, no PTR"
    echo -e "   record, home-IP blocklists) — consider an SMTP relay for outbound mail."
else
    echo -e "Your applications will take a few minutes to boot up (certificates are self-signed on a local deployment)."
    echo ""
    echo -e "${YELLOW}LAN name resolution — required before the URLs below work:${NC}"
    echo -e "Nothing manages DNS for a LAN-only deployment. Point the app hostnames at your"
    echo -e "server (${CYAN}${SERVER_IP}${NC}) in your router's DNS / Pi-hole, or add this line to the"
    echo -e "/etc/hosts of every device that should access the cloud:"
    echo ""
    cat "$HOSTS_SNIPPET"
    echo ""
    echo -e "(The line above is saved to ${CYAN}${HOSTS_SNIPPET}${NC}. To apply it on this machine:"
    echo -e " ${YELLOW}sudo tee -a /etc/hosts < ${HOSTS_SNIPPET}${NC})"
    echo ""
    echo -e "${YELLOW}Notes for local deployments:${NC}"
    echo -e " - Browsers will warn about the self-signed certificates — this is expected."
    echo -e " - Stalwart mail deploys, but external mail delivery needs a public IP, PTR"
    echo -e "   record and open port 25 — it will not work from behind a home NAT."
fi
echo ""
echo -e "Kubernetes Access (kubeconfig):"
echo -e "  Saved to:                  ${CYAN}${KUBECONFIG_LOCAL}${NC}"
echo -e "  To use with kubectl:       ${YELLOW}export KUBECONFIG=${KUBECONFIG_LOCAL}${NC}"
echo -e "                             (or link it: ln -sf ${KUBECONFIG_LOCAL} ~/.kube/config)"
echo ""
echo -e "Here are your auto-generated admin credentials. Save them somewhere safe!"
echo -e "Keycloak Admin (admin):      ${CYAN}${KC_PASS}${NC}"
echo -e "ArgoCD Admin (admin):        ${CYAN}${ARGOCD_PASS}${NC}"
echo -e "Grafana Admin (admin):       ${CYAN}${GRAFANA_PASS}${NC}"
echo -e "Bulk Invite Secret:          ${CYAN}${INVITE_SECRET}${NC}"
echo ""
echo -e "Note: Passwords for optional apps (Nextcloud, Immich, Forgejo) and Stalwart are automatically"
echo -e "generated securely upon installation. You can retrieve them via kubectl later."
echo ""
echo -e "Application URLs (installed apps will be available here):"
echo -e "  Dashboard:         ${CYAN}https://dashboard${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Identity:          ${CYAN}https://identity${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Mail (Stalwart):   ${CYAN}https://mail${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Files (Nextcloud): ${CYAN}https://files${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Photos (Immich):   ${CYAN}https://photos${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Git (Forgejo):     ${CYAN}https://git${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Webmail (Bulwark): ${CYAN}https://webmail${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Monitoring:        ${CYAN}https://monitoring${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Whiteboard:        ${CYAN}https://whiteboard${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Meet:              ${CYAN}https://meet${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Office:            ${CYAN}https://office${ENV_EXT}.${DOMAIN}${NC}"
echo -e "  Plan:              ${CYAN}https://plan${ENV_EXT}.${DOMAIN}${NC}"
echo ""
echo -e "ArgoCD Dashboard:            ${CYAN}https://localhost:8080${NC} (requires port-forward)"
echo -e "  To port-forward:           ${YELLOW}kubectl port-forward svc/argocd-server -n argocd 8080:443${NC}"
echo -e "${GREEN}======================================================${NC}"

# Watch the ArgoCD rollout until every application is Synced+Healthy.
# ArgoCD parks a sync permanently once its retries are exhausted, so any app
# whose operation ended in Failed/Error gets re-kicked automatically.
if command -v kubectl >/dev/null 2>&1; then
    echo -e "${CYAN}Watching application rollout (up to 40 minutes; safe to Ctrl+C — the cluster continues on its own)...${NC}"
    export KUBECONFIG="$KUBECONFIG_LOCAL"
    CONVERGED=false
    for i in $(seq 1 120); do
        for app in $(kubectl get application -n argocd -o jsonpath='{range .items[?(@.status.operationState.phase=="Failed")]}{.metadata.name}{" "}{end}' 2>/dev/null) \
                   $(kubectl get application -n argocd -o jsonpath='{range .items[?(@.status.operationState.phase=="Error")]}{.metadata.name}{" "}{end}' 2>/dev/null); do
            echo -e "  ${YELLOW}Sync of '$app' gave up — retriggering...${NC}"
            # A manually patched operation does NOT inherit the app's
            # spec.syncPolicy.syncOptions — a bare {"sync":{}} drops
            # SkipDryRunOnMissingResource and the sync then aborts on any
            # CR whose CRD isn't installed yet (e.g. AlertmanagerConfig
            # before kube-prometheus-stack). Copy the options explicitly.
            APP_SYNC_OPTS=$(kubectl get application "$app" -n argocd -o json 2>/dev/null \
                | python3 -c 'import json,sys; print(json.dumps((json.load(sys.stdin)["spec"].get("syncPolicy") or {}).get("syncOptions") or []))' 2>/dev/null)
            [ -z "$APP_SYNC_OPTS" ] && APP_SYNC_OPTS='[]'
            kubectl patch application "$app" -n argocd --type merge \
                -p '{"operation":{"initiatedBy":{"username":"installer-watchdog"},"sync":{"syncOptions":'"$APP_SYNC_OPTS"'}}}' >/dev/null 2>&1 || true
        done

        # Get all ArgoCD apps that are not healthy
        UNHEALTHY=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[?(@.status.health.status!="Healthy")].metadata.name}' 2>/dev/null)
        HEALTHY=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[?(@.status.health.status=="Healthy")].metadata.name}' 2>/dev/null | wc -w)
        TOTAL=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | wc -w)
        ROOT_STATE=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get app smallworlds-root -n argocd -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null)
        if [ "$TOTAL" -gt 1 ] && [ "$HEALTHY" -eq "$TOTAL" ] && [ "$ROOT_STATE" = "Synced/Healthy" ]; then
            echo -e "${GREEN}All $TOTAL applications are Healthy!${NC}"
            CONVERGED=true
            break
        fi
        echo -e "  [$i/120] $HEALTHY/$TOTAL apps healthy (root: ${ROOT_STATE:-pending})"
        sleep 20
    done
    if [ "$CONVERGED" = false ]; then
        echo -e "${YELLOW}Rollout did not fully converge within 40 minutes. Inspect with:${NC}"
        echo -e "  kubectl get application -n argocd"
    fi
else
    echo -e "${YELLOW}kubectl not found locally — skipping rollout watch. Check progress with the ArgoCD dashboard.${NC}"
fi

# Open the dashboard
DASHBOARD_URL="https://dashboard${ENV_EXT}.${DOMAIN}"
echo ""
echo -e "${YELLOW}Please note: The infrastructure is currently being provisioned in the background.${NC}"
echo -e "${YELLOW}It may take 5-10 minutes for all services to come online and for SSL certificates to be issued.${NC}"
echo -e "${YELLOW}If you see a 'Bad Gateway' or SSL warning, simply wait a few minutes and refresh.${NC}"
if [[ "$DEPLOY_TARGET" == "local" ]]; then
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        echo -e "${YELLOW}Remember: the dashboard only resolves once the DDNS job has created the DNS records (~5 min).${NC}"
    else
        echo -e "${YELLOW}Remember: the dashboard only resolves once the hosts entries above are in place.${NC}"
    fi
fi
echo ""
echo -e "${CYAN}Opening your dashboard: ${DASHBOARD_URL}${NC}"
python3 -c "import webbrowser; webbrowser.open('${DASHBOARD_URL}')" >/dev/null 2>&1 || echo -e "${CYAN}Please manually navigate to: ${DASHBOARD_URL}${NC}"
