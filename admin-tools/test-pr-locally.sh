#!/usr/bin/env bash
set -eo pipefail

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

if [ -z "$1" ]; then
    echo -e "${RED}Usage: $0 <branch-name>${NC}"
    echo -e "Example: $0 renovate/nextcloud-9.x"
    exit 1
fi

TARGET_BRANCH="$1"

if [ -z "$HCLOUD_TOKEN" ]; then
    echo -e "${RED}Error: HCLOUD_TOKEN environment variable is not set.${NC}"
    echo -e "Please set it before running this script: export HCLOUD_TOKEN=your_token"
    exit 1
fi

echo -e "${CYAN}╔══════════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║     SmallWorlds Local Ephemeral Staging Runner       ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════════════╝${NC}"
echo -e "Target Branch: ${YELLOW}$TARGET_BRANCH${NC}"

# Go to repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

# Ensure target branch is available locally
git fetch origin "$TARGET_BRANCH" || true
git fetch origin main || true

# Save current branch so we can restore it later
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

echo -e "${CYAN}Checking out $TARGET_BRANCH...${NC}"
git checkout "$TARGET_BRANCH"

# 1. Analyze Diff
echo -e "${CYAN}Analyzing differences from main...${NC}"
CHANGED_FILES=$(git diff --name-only origin/main...HEAD)

CORE_CHANGED=false
if echo "$CHANGED_FILES" | grep -qE '^infrastructure/kubernetes/(apps|bases)/' || echo "$CHANGED_FILES" | grep -qE '^infrastructure/terraform/'; then
    CORE_CHANGED=true
fi

MODIFIED_TENANTS=$(echo "$CHANGED_FILES" | grep '^infrastructure/kubernetes/tenants/' | awk -F'/' '{print $4}' | sort -u || true)

# 2. Build Kustomization
echo -e "${CYAN}Building dynamic Kustomization...${NC}"
cat << 'EOF' > infrastructure/kubernetes/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespaces.yaml
  - apps/cert-manager.yaml
  - apps/cloudnative-pg.yaml
  - apps/garage.yaml
  - apps/persistent-storage.yaml
  - apps/traefik.yaml
  - apps/keycloak.yaml
EOF

TEST_FILTER=""
if [ "$CORE_CHANGED" = true ]; then
    echo -e "${YELLOW}Core infrastructure changed. Deploying ALL applications.${NC}"
    for app in infrastructure/kubernetes/apps/*.yaml; do
        basename=$(basename "$app")
        if ! grep -q "apps/$basename" infrastructure/kubernetes/kustomization.yaml; then
            echo "  - apps/$basename" >> infrastructure/kubernetes/kustomization.yaml
        fi
    done
else
    echo -e "${GREEN}Only specific tenants changed. Selectively deploying...${NC}"
    for tenant in $MODIFIED_TENANTS; do
        if [ -f "infrastructure/kubernetes/apps/${tenant}.yaml" ]; then
            echo -e "  Adding tenant: ${YELLOW}$tenant${NC}"
            echo "  - apps/${tenant}.yaml" >> infrastructure/kubernetes/kustomization.yaml
            TEST_FILTER="$TEST_FILTER $tenant"
        fi
    done
fi

# Override Target Revisions locally
echo -e "${CYAN}Overriding targetRevision to $TARGET_BRANCH locally...${NC}"
find infrastructure/kubernetes/apps -name '*.yaml' -type f -exec sed -i "s/targetRevision: HEAD/targetRevision: $TARGET_BRANCH/g" {} +
find infrastructure/kubernetes/apps -name '*.yaml' -type f -exec sed -i "s/targetRevision: main/targetRevision: $TARGET_BRANCH/g" {} +

# Generate ephemeral SSH key
TEMP_SSH_KEY=$(mktemp)
ssh-keygen -t ed25519 -f "$TEMP_SSH_KEY" -N "" -q
export TF_VAR_ssh_public_key_path="${TEMP_SSH_KEY}.pub"
export TF_VAR_github_pr_branch="$TARGET_BRANCH"

# Setup Cleanup Trap
cleanup() {
    echo -e "\n${CYAN}==========================================${NC}"
    echo -e "${CYAN}          Starting Cleanup Phase          ${NC}"
    echo -e "${CYAN}==========================================${NC}"
    
    echo -e "${YELLOW}Cleaning up /etc/hosts... (May prompt for sudo)${NC}"
    sudo sed -i '/smallworlds\.network/d' /etc/hosts

    echo -e "${YELLOW}Destroying Hetzner VM...${NC}"
    cd "$REPO_ROOT/infrastructure/terraform-staging"
    terraform destroy -auto-approve || true
    
    echo -e "${YELLOW}Cleaning up SSH keys and temporary files...${NC}"
    rm -f "$TEMP_SSH_KEY" "${TEMP_SSH_KEY}.pub"
    
    echo -e "${YELLOW}Restoring original git state...${NC}"
    cd "$REPO_ROOT"
    git checkout -- infrastructure/kubernetes/kustomization.yaml
    git checkout -- infrastructure/kubernetes/apps/
    git checkout "$ORIGINAL_BRANCH"

    echo -e "${GREEN}Cleanup complete!${NC}"
}
trap cleanup EXIT

# 3. Provision VM
echo -e "\n${CYAN}[1/3] Provisioning Ephemeral Hetzner VM...${NC}"
cd "$REPO_ROOT/infrastructure/terraform-staging"
terraform init
terraform apply -auto-approve

SERVER_IP=$(terraform output -raw server_ipv4)
echo -e "${GREEN}VM provisioned at: $SERVER_IP${NC}"

# 4. Fetch Kubeconfig
echo -e "\n${CYAN}[2/3] Waiting for K3s initialization...${NC}"
timeout 300 bash -c "until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i $TEMP_SSH_KEY root@$SERVER_IP 'test -f /root/k3s.yaml' 2>/dev/null; do sleep 10; done"
scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$TEMP_SSH_KEY" root@$SERVER_IP:/root/k3s.yaml "$REPO_ROOT/kubeconfig-staging.yaml"
export KUBECONFIG="$REPO_ROOT/kubeconfig-staging.yaml"

echo -e "${GREEN}K3s is ready!${NC}"

# 5. Deploy Apps
cd "$REPO_ROOT"
echo -e "\n${CYAN}[3/3] Deploying Applications via ArgoCD...${NC}"
kubectl apply -k infrastructure/kubernetes

echo -e "${YELLOW}Waiting for pods to be Ready (this may take up to 10 minutes)...${NC}"
sleep 15
kubectl wait --for=condition=Ready pod --all -n default --timeout=600s || true
kubectl wait --for=condition=Ready pod --all -n keycloak --timeout=600s || true

# 6. Setup Local DNS
echo -e "\n${CYAN}Setting up local DNS routing... (May prompt for sudo)${NC}"
sudo sed -i '/smallworlds\.network/d' /etc/hosts
echo "$SERVER_IP identity.smallworlds.network files.smallworlds.network webmail.smallworlds.network photos.smallworlds.network git.smallworlds.network meet.smallworlds.network" | sudo tee -a /etc/hosts >/dev/null

# 7. Run E2E Tests
echo -e "\n${CYAN}Starting E2E Smoke Tests...${NC}"
cd e2e-tests
npm ci
npx playwright install chromium
./run-smoke-tests.sh smallworlds.network "e2e-dummy-pass" "$TEST_FILTER"

echo -e "\n${GREEN}Success! Tests completed.${NC}"
