#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CACHE_FILE="$SCRIPT_DIR/../.smallworlds-cache.env"

if [ -f "$CACHE_FILE" ]; then
    source "$CACHE_FILE"
else
    echo "Error: .smallworlds-cache.env not found. Needed for terraform credentials."
    exit 1
fi

if [ -z "$ENV_EXT" ]; then
    ENV_EXT=$(sed -n 's/^[[:space:]]*env_ext[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$SCRIPT_DIR/../infrastructure/terraform/terraform.tfvars" 2>/dev/null | head -1)
fi

CLUSTER_LABEL="production"
if [ -n "$ENV_EXT" ]; then
    CLUSTER_LABEL="${ENV_EXT#.}"
fi

echo "=========================================================="
echo "⚠️  WARNING: CLUSTER DESTRUCTION INITIATED ('$CLUSTER_LABEL' cluster)"
echo "This will back up your certificates and then run terraform destroy"
echo "to COMPLETELY delete all cloud resources for this cluster."
echo "=========================================================="
read -p "Are you absolutely sure? (Type DESTROY to continue): " confirm
if [ "$confirm" != "DESTROY" ]; then
  echo "Aborting."
  exit 1
fi

echo ""
echo "1. Backing up Let's Encrypt certificates to this machine..."
ENV_EXT="$ENV_EXT" "$SCRIPT_DIR/backup-certs-to-laptop.sh"

echo ""
echo "2. Preparing Terraform variables..."
TFVARS_FILE=$(mktemp)
cat <<EOF > "$TFVARS_FILE"
domain_name  = "${DOMAIN}"
env_ext      = "${ENV_EXT}"
git_url      = "${GITOPS_REPO_URL}"
git_username = "${GITOPS_REPO_USER}"
git_password = "${GITOPS_REPO_TOKEN}"
hcloud_token = "${HCLOUD_TOKEN}"
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
