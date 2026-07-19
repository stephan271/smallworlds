#!/bin/bash
set -e

# Fully destroys a cluster's infrastructure. Branches on DEPLOY_TARGET
# (cached by smallworlds-init.sh):
#   hetzner  terraform destroy (cloud VM, volume, DNS, firewall)
#   local    bootstrap-local-node.sh --uninstall --purge-data over SSH on
#            LOCAL_SSH_TARGET — there is no Terraform state for this target,
#            so terraform destroy would be a silent no-op against it.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CACHE_FILE="$SCRIPT_DIR/../.smallworlds-cache.env"

if [ -f "$CACHE_FILE" ]; then
    source "$CACHE_FILE"
else
    echo "Error: .smallworlds-cache.env not found. Needed for deployment credentials."
    exit 1
fi

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER_LABEL=$(cluster_label "$CUR_ENV_EXT")
DEPLOY_TARGET="${DEPLOY_TARGET:-hetzner}"

echo "=========================================================="
echo "⚠️  WARNING: CLUSTER DESTRUCTION INITIATED ('$CLUSTER_LABEL' cluster, '$DEPLOY_TARGET' target)"
if [ "$DEPLOY_TARGET" = "local" ]; then
    echo "This will back up your certificates, then uninstall k3s and PURGE ALL"
    echo "DATA on ${LOCAL_SSH_TARGET:-<LOCAL_SSH_TARGET not set>}. No cloud resources are involved."
else
    echo "This will back up your certificates and then run terraform destroy"
    echo "to COMPLETELY delete all cloud resources for this cluster."
fi
echo "=========================================================="
read -p "Are you absolutely sure? (Type DESTROY to continue): " confirm
if [ "$confirm" != "DESTROY" ]; then
  echo "Aborting."
  exit 1
fi

echo ""
echo "1. Backing up Let's Encrypt certificates to this machine..."
if [ "$DEPLOY_TARGET" = "local" ]; then
    # Local nodes aren't reachable via the Hetzner-style root@<ip> SSH that
    # backup-certs-to-laptop.sh falls back to — point it at the cached
    # kubeconfig instead of letting it auto-fetch.
    export KUBECONFIG="$(kubeconfig_path "$CLUSTER_LABEL")"
fi
ENV_EXT="$CUR_ENV_EXT" "$SCRIPT_DIR/backup-certs-to-laptop.sh"

if [ "$DEPLOY_TARGET" = "local" ]; then
    if [ -z "$LOCAL_SSH_TARGET" ]; then
        echo "Error: LOCAL_SSH_TARGET not set in $CACHE_FILE — cannot reach the local node."
        exit 1
    fi

    echo ""
    echo "2. Uninstalling k3s and purging all data on ${LOCAL_SSH_TARGET}..."
    BOOTSTRAP_SRC="$SCRIPT_DIR/../infrastructure/local/bootstrap-local-node.sh"
    if [ "$LOCAL_SSH_TARGET" = "localhost" ]; then
        sudo bash "$BOOTSTRAP_SRC" --uninstall --purge-data
    else
        SUDO_PREFIX="sudo "
        [[ "$LOCAL_SSH_TARGET" == root@* ]] && SUDO_PREFIX=""
        scp $SSH_OPTS "$BOOTSTRAP_SRC" "$LOCAL_SSH_TARGET:/tmp/smallworlds-bootstrap-node.sh" >/dev/null
        ssh -t $SSH_OPTS "$LOCAL_SSH_TARGET" "${SUDO_PREFIX}bash /tmp/smallworlds-bootstrap-node.sh --uninstall --purge-data"
        ssh $SSH_OPTS "$LOCAL_SSH_TARGET" "rm -f /tmp/smallworlds-bootstrap-node.sh" 2>/dev/null || true
    fi

    echo ""
    echo "✅ Local cluster on ${LOCAL_SSH_TARGET} has been uninstalled and all data purged."
    echo "Your certificates are safely backed up under ~/.smallworlds/cert-backups/$CLUSTER_LABEL/"
    echo "To rebuild, run: ./smallworlds-init.sh"
else
    echo ""
    echo "2. Preparing Terraform variables..."
    # ssh_key_id has no default (see main.tf locals) — terraform destroy
    # still needs a value to evaluate it, though its correctness doesn't
    # matter for a destroy. 0 is a harmless placeholder if the key was never
    # found (e.g. destroying a cluster whose key was already removed).
    SSH_KEY_ID=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/ssh_keys?name=SmallWorlds%20Admin%20Key" | grep -o '"id":[0-9]*' | head -1 | grep -o '[0-9]*')
    SSH_KEY_ID="${SSH_KEY_ID:-0}"
    TFVARS_FILE=$(mktemp)
    cat <<EOF > "$TFVARS_FILE"
domain_name  = "${DOMAIN}"
env_ext      = "${CUR_ENV_EXT}"
git_url      = "${GITOPS_REPO_URL}"
git_username = "${GITOPS_REPO_USER}"
git_password = "${GITOPS_REPO_TOKEN}"
hcloud_token = "${HCLOUD_TOKEN}"
ssh_key_id   = ${SSH_KEY_ID}
EOF

    echo ""
    echo "3. Destroying infrastructure via Terraform..."
    cd "$SCRIPT_DIR/../infrastructure/terraform"
    export HCLOUD_TOKEN="$HCLOUD_TOKEN"

    # Temporarily disable prevent_destroy for the volume to allow full deletion
    sed -i 's/prevent_destroy = true/prevent_destroy = false/g' main.tf

    terraform destroy -var-file="$TFVARS_FILE" -auto-approve

    # Restore main.tf to its original state so git is clean
    git checkout main.tf 2>/dev/null || git restore main.tf 2>/dev/null || true

    rm -f "$TFVARS_FILE"

    echo ""
    echo "✅ Cluster has been completely destroyed."
    echo "Your certificates are safely backed up under ~/.smallworlds/cert-backups/$CLUSTER_LABEL/"
    echo "To restore them into a freshly rebuilt cluster, run: ./admin-tools/restore-certs-from-laptop.sh"
fi
