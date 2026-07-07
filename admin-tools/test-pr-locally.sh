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
    echo -e "Set KEEP_VM=1 to skip destroying the staging VM on exit (for debugging)."
    exit 1
fi

TARGET_BRANCH="$1"

if [ -z "$HCLOUD_TOKEN" ]; then
    echo -e "${RED}Error: HCLOUD_TOKEN environment variable is not set.${NC}"
    echo -e "Please set it before running this script: export HCLOUD_TOKEN=your_token"
    exit 1
fi
export TF_VAR_hcloud_token="$HCLOUD_TOKEN"

# Boot from the golden image (preloaded k3s + container images) if one exists
GOLDEN_COUNT=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
    "https://api.hetzner.cloud/v1/images?type=snapshot&label_selector=smallworlds-golden%3Dtrue" \
    | grep -c '"id"' || true)
if [ "$GOLDEN_COUNT" -gt 0 ]; then
    echo -e "${GREEN}Golden image found — fast staging boot enabled.${NC}"
    export TF_VAR_use_golden_image=true
fi

echo -e "${CYAN}╔══════════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║     SmallWorlds Local Ephemeral Staging Runner       ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════════════╝${NC}"
echo -e "Target Branch: ${YELLOW}$TARGET_BRANCH${NC}"

# Ask for sudo upfront to avoid timeout during trap
echo -e "\n${YELLOW}We need sudo access to modify /etc/hosts for the tests. Please authenticate now:${NC}"
sudo -v
# Keep-alive: update existing sudo time stamp until script has finished
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &

# Go to repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

# Ensure target branch is available locally
git fetch origin "$TARGET_BRANCH" || true
git fetch origin main || true

# Save current branch so we can restore it later
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

echo -e "${CYAN}Checking out origin/$TARGET_BRANCH...${NC}"
git checkout -B "$TARGET_BRANCH" "origin/$TARGET_BRANCH"

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
find infrastructure/kubernetes/apps -name '*.yaml' -type f -exec sed -i "s@targetRevision: HEAD@targetRevision: $TARGET_BRANCH@g" {} +
find infrastructure/kubernetes/apps -name '*.yaml' -type f -exec sed -i "s@targetRevision: main@targetRevision: $TARGET_BRANCH@g" {} +

# Fix node affinity for local storage in the staging cluster
echo -e "${CYAN}Overriding nodeAffinity for staging node...${NC}"
sed -i "s/cc-pilot-node-01/cc-staging-node-01/g" infrastructure/kubernetes/apps/persistent-storage.yaml

# Generate ephemeral SSH key
TEMP_SSH_KEY=$(mktemp)
ssh-keygen -t ed25519 -f "$TEMP_SSH_KEY" -N "" -q
export TF_VAR_ssh_public_key_path="${TEMP_SSH_KEY}.pub"
export TF_VAR_github_pr_branch="$TARGET_BRANCH"

# Setup Cleanup Trap
cleanup() {
    local EXIT_CODE=$?
    
    echo -e "\n${CYAN}==========================================${NC}"
    echo -e "${CYAN}          Starting Cleanup Phase          ${NC}"
    echo -e "${CYAN}==========================================${NC}"
    
    if [ "${KEEP_VM:-0}" = "1" ]; then
        echo -e "${YELLOW}KEEP_VM=1 set: skipping VM destruction so you can debug.${NC}"
        echo -e "  kubectl:      export KUBECONFIG=$REPO_ROOT/kubeconfig-staging.yaml"
        echo -e "  ssh:          ssh -i $TEMP_SSH_KEY root@\$(cd $REPO_ROOT/infrastructure/terraform-staging && terraform output -raw server_ipv4)"
        echo -e "  destroy VM:   cd $REPO_ROOT/infrastructure/terraform-staging && terraform destroy -auto-approve"
        echo -e "  clean hosts:  sudo sed -i '/smallworlds\\.network/d' /etc/hosts"
    else
        echo -e "${YELLOW}Cleaning up /etc/hosts... (May prompt for sudo)${NC}"
        sudo sed -i '/smallworlds\.network/d' /etc/hosts

        if [ -d "$REPO_ROOT/infrastructure/terraform-staging" ]; then
            echo -e "${YELLOW}Destroying Hetzner VM...${NC}"
            cd "$REPO_ROOT/infrastructure/terraform-staging"
            terraform destroy -auto-approve || true
        else
            echo -e "${YELLOW}Skipping Terraform destroy (directory missing on this branch)...${NC}"
        fi

        echo -e "${YELLOW}Cleaning up SSH keys and temporary files...${NC}"
        rm -f "$TEMP_SSH_KEY" "${TEMP_SSH_KEY}.pub"
    fi
    
    echo -e "${YELLOW}Restoring original git state...${NC}"
    cd "$REPO_ROOT"
    git checkout -- infrastructure/kubernetes/kustomization.yaml
    git checkout -- infrastructure/kubernetes/apps/
    git checkout "$ORIGINAL_BRANCH"

    echo -e "\n=========================================="
    if [ $EXIT_CODE -eq 0 ]; then
        echo -e "${GREEN}✅ SUCCESS: All tests passed and cleanup is complete!${NC}"
    else
        echo -e "${RED}❌ FAILED: The PR tests failed with exit code $EXIT_CODE!${NC}"
        echo -e "${YELLOW}To see exactly what went wrong, you can view the test report:${NC}"
        echo -e "  cd e2e-tests && npx playwright show-report reports/html"
    fi
    echo -e "==========================================\n"
    
    exit $EXIT_CODE
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

# Inject required initial secrets for the staging environment (similar to smallworlds-init.sh)
cat <<EOF | kubectl apply -f -
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
stringData:
  rpcSecret: "$(openssl rand -hex 32)"
  adminToken: "$(openssl rand -hex 32)"
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
stringData:
  admin-password: "e2e-dummy-pass"
  bulk-invite-secret: "staging-invite-secret"
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
stringData:
  HCLOUD_TOKEN: "dummy"
  DOMAIN: "smallworlds.network"
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
stringData:
  admin-user: "admin"
  admin-password: "e2e-dummy-pass"
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
stringData:
  url: "https://github.com/stephan271/smallworlds.git"
  username: "dummy"
  password: "dummy"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: smallworlds-global-config
  namespace: default
data:
  ADMIN_EMAIL: "admin@smallworlds.network"
  DOMAIN: "smallworlds.network"
EOF

kubectl apply -k infrastructure/kubernetes

echo -e "${YELLOW}Waiting for ArgoCD to sync and deploy pods (this may take up to 15 minutes)...${NC}"
sleep 30

# Wait for all ArgoCD applications to become Healthy
echo -e "${CYAN}Waiting for all ArgoCD applications to reach Healthy state (this may take up to 30 minutes)...${NC}"
for i in {1..180}; do
    # Fetch apps that are not Healthy
    UNHEALTHY=$(kubectl get application -n argocd -o jsonpath='{range .items[?(@.status.health.status!="Healthy")]}{.metadata.name}{" "}{end}' 2>/dev/null)
    
    if [ -z "$UNHEALTHY" ]; then
        echo -e "${GREEN}All ArgoCD applications are Healthy and Synced!${NC}"
        break
    fi
    
    echo -e "Still waiting for apps to become Healthy: ${YELLOW}${UNHEALTHY}${NC}"
    sleep 10
done

if [ -n "$UNHEALTHY" ]; then
    echo -e "${RED}Timeout reached! The following apps never became healthy: ${UNHEALTHY}${NC}"
    echo -e "${YELLOW}Gathering debug information for unhealthy namespaces...${NC}"
    for app in $UNHEALTHY; do
        ns=$(kubectl get application $app -n argocd -o jsonpath='{.spec.destination.namespace}')
        if [ -n "$ns" ]; then
            echo -e "
--- POD STATUS IN $ns ---"
            kubectl get pods -n "$ns"
            echo -e "
--- EVENTS IN $ns ---"
            kubectl get events -n "$ns" --sort-by='.lastTimestamp' | tail -n 15
        fi
    done
fi

# As a final safety check, ensure deployments and statefulsets are available
for ns in $(kubectl get application -n argocd -o jsonpath='{range .items[*]}{.spec.destination.namespace}{" "}{end}' | sort -u); do
    if [ -n "$ns" ]; then
        kubectl wait --for=condition=Available deployment --all -n "$ns" --timeout=60s 2>/dev/null || true
        kubectl wait --for=condition=Ready statefulset --all -n "$ns" --timeout=60s 2>/dev/null || true
    fi
done

# 6. Setup Local DNS
echo -e "\n${CYAN}Setting up local DNS routing... (May prompt for sudo)${NC}"
sudo sed -i '/smallworlds\.network/d' /etc/hosts
# Keep this list in sync with the subdomains used in e2e-tests/tests/*.spec.ts
echo "$SERVER_IP identity.smallworlds.network files.smallworlds.network webmail.smallworlds.network photos.smallworlds.network git.smallworlds.network meet.smallworlds.network whiteboard.smallworlds.network" | sudo tee -a /etc/hosts >/dev/null

# 7. Run E2E Tests
echo -e "\n${CYAN}Starting E2E Smoke Tests...${NC}"
cd e2e-tests
npm ci
npx playwright install chromium

# Staging uses a self-signed ClusterIssuer; Node's fetch (used by the user
# provisioning setup) rejects those certs, unlike Playwright itself.
export NODE_TLS_REJECT_UNAUTHORIZED=0
./run-smoke-tests.sh smallworlds.network "e2e-dummy-pass" "$TEST_FILTER"

echo -e "\n${GREEN}Success! Tests completed.${NC}"
