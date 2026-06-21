#!/usr/bin/env bash
set -e

# Colors for pretty output
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

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
            read -p "$prompt_text [$current_val]: " input_val
        fi
        
        if [[ -z "$input_val" ]]; then
            eval "$var_name=\"$current_val\""
        else
            eval "$var_name=\"$input_val\""
        fi
    else
        if [[ "$is_secret" == "true" ]]; then
            read -s -p "$prompt_text: " input_val
            echo ""
        else
            read -p "$prompt_text: " input_val
        fi
        eval "$var_name=\"$input_val\""
    fi
}

echo -e "${YELLOW}Gathering Configuration...${NC}"
ask_with_default "1. Enter your target domain (e.g. smallworlds.network)" "DOMAIN" "false"
ask_with_default "2. Enter the admin email address" "ADMIN_EMAIL" "false"

# Ensure ONBOARDING_MODE has a valid default if empty
if [[ -z "$ONBOARDING_MODE" ]]; then
    ONBOARDING_MODE="invitation"
fi
ask_with_default "3. Select onboarding mode (invitation or self-registration)" "ONBOARDING_MODE" "false"

echo ""
echo -e "${YELLOW}Hetzner Configuration${NC}"
ask_with_default "4. Paste your Hetzner Cloud API Token" "HCLOUD_TOKEN" "true"

echo ""
echo -e "${YELLOW}GitOps Repository Configuration${NC}"
ask_with_default "5. Enter your Git repository URL (e.g., https://github.com/my-community/config.git)" "GITOPS_REPO_URL" "false"
ask_with_default "6. Enter your Git username" "GITOPS_REPO_USER" "false"
ask_with_default "7. Paste your Git Access Token" "GITOPS_REPO_TOKEN" "true"

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

# Save values to cache for next time
cat <<EOF > "$CACHE_FILE"
DOMAIN="${DOMAIN}"
ADMIN_EMAIL="${ADMIN_EMAIL}"
ONBOARDING_MODE="${ONBOARDING_MODE}"
HCLOUD_TOKEN="${HCLOUD_TOKEN}"
GITOPS_REPO_URL="${GITOPS_REPO_URL}"
GITOPS_REPO_USER="${GITOPS_REPO_USER}"
GITOPS_REPO_TOKEN="${GITOPS_REPO_TOKEN}"
EOF
chmod 600 "$CACHE_FILE"

echo ""

echo -e "${CYAN}Generating configuration...${NC}"

# Update ONBOARDING_MODE in the job manifest
sed -i -E "s/value: \"(invitation|self-registration)\"/value: \"$ONBOARDING_MODE\"/g" infrastructure/kubernetes/tenants/keycloak/realm-config-job.yaml

# Export Hetzner Token as environment variable so Terraform can find it
export HCLOUD_TOKEN=$HCLOUD_TOKEN

# Set Terraform Git variables
TF_GIT_USER="${GITOPS_REPO_USER}"
TF_GIT_TOKEN="${GITOPS_REPO_TOKEN}"

# 2. Generate temporary tfvars file
TFVARS_FILE="/tmp/smallworlds-${DOMAIN}.tfvars"
cat <<EOF > "$TFVARS_FILE"
domain_name       = "${DOMAIN}"
git_url        = "${GITOPS_REPO_URL}"
git_username   = "${TF_GIT_USER}"
git_password   = "${TF_GIT_TOKEN}"
hcloud_token      = "${HCLOUD_TOKEN}"

EOF

# 3. Execute Terraform
echo -e "${CYAN}Initializing infrastructure... This will take a few minutes.${NC}"
cd infrastructure/terraform

terraform init -input=false > /dev/null
terraform apply -var-file="$TFVARS_FILE" -auto-approve

# 4. Capture Outputs
SERVER_IP=$(terraform output -raw server_ipv4)
KC_PASS=$(terraform output -raw keycloak_admin_password)
NC_PASS=$(terraform output -raw nextcloud_admin_password)
IM_PASS=$(terraform output -raw immich_admin_password)
MAIL_PASS=$(terraform output -raw stalwart_admin_password)
GIT_PASS=$(terraform output -raw forgejo_admin_password)
INVITE_SECRET=$(terraform output -raw bulk_invite_secret)

# 5. Retrieve Kubeconfig
echo -e "${CYAN}Waiting for SSH to be available on $SERVER_IP...${NC}"
while ! timeout 2 bash -c "</dev/tcp/$SERVER_IP/22" 2>/dev/null; do
    sleep 2
done

echo -e "${CYAN}Waiting for K3s to generate kubeconfig on the remote node...${NC}"
until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 root@"$SERVER_IP" "[ -f /etc/rancher/k3s/k3s.yaml ]" 2>/dev/null; do
    sleep 2
done

KUBECONFIG_LOCAL="../../k3s_kubeconfig.yaml"
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@"$SERVER_IP" "cat /etc/rancher/k3s/k3s.yaml" > "$KUBECONFIG_LOCAL" 2>/dev/null
sed -i "s|127.0.0.1|$SERVER_IP|g" "$KUBECONFIG_LOCAL"
chmod 600 "$KUBECONFIG_LOCAL"

echo -e "${CYAN}Retrieving ArgoCD initial admin password...${NC}"
until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 root@"$SERVER_IP" "kubectl -n argocd get secret argocd-initial-admin-secret >/dev/null 2>&1" 2>/dev/null; do
    sleep 2
done
ARGOCD_PASS=$(ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@"$SERVER_IP" "kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath=\"{.data.password}\" | base64 -d" 2>/dev/null)

echo ""
echo -e "${GREEN}======================================================${NC}"
echo -e "${GREEN}             Deployment Successful!                   ${NC}"
echo -e "${GREEN}======================================================${NC}"
echo ""
echo -e "Your applications will take a few minutes to boot up and fetch their SSL certificates."
echo ""
echo -e "Kubernetes Access (kubeconfig):"
echo -e "  Saved to:                  ${CYAN}./k3s_kubeconfig.yaml${NC}"
echo -e "  To use with kubectl:       ${YELLOW}export KUBECONFIG=\$PWD/k3s_kubeconfig.yaml${NC}"
echo -e "                             (or link it: ln -sf \$PWD/k3s_kubeconfig.yaml ~/.kube/config)"
echo ""
echo -e "Here are your auto-generated admin credentials. Save them somewhere safe!"
echo -e "Keycloak Admin (admin):      ${CYAN}${KC_PASS}${NC}"
echo -e "Nextcloud Admin (admin):     ${CYAN}${NC_PASS}${NC}"
echo -e "Immich Admin (${ADMIN_EMAIL}):     ${CYAN}${IM_PASS}${NC}"
echo -e "Stalwart Mail Admin (admin): ${CYAN}${MAIL_PASS}${NC}"
echo -e "Forgejo Git Admin (gitadmin):   ${CYAN}${GIT_PASS}${NC}"
echo -e "ArgoCD Admin (admin):        ${CYAN}${ARGOCD_PASS}${NC}"
echo -e "Bulk Invite Secret:          ${CYAN}${INVITE_SECRET}${NC}"
echo ""
echo -e "ArgoCD Dashboard:            ${CYAN}https://localhost:8080${NC} (requires port-forward)"
echo -e "  To port-forward:           ${YELLOW}kubectl port-forward svc/argocd-server -n argocd 8080:443${NC}"
echo -e "${GREEN}======================================================${NC}"

# Cleanup
rm "$TFVARS_FILE"
