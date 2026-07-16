#!/bin/bash
set -e

# Prepares a clean cluster rebuild: backs up the Let's Encrypt certificates to
# your laptop (see backup-certs-to-laptop.sh), then wipes ALL data on the
# server's persistent volume. After `terraform destroy` + `terraform apply`,
# run restore-certs-from-laptop.sh to inject the certificates into the new
# cluster before cert-manager tries to re-issue them.
#
# Environment (production vs -dev) is auto-detected — see lib/cluster-env.sh.
#
# Usage:
#   ./admin-tools/prepare-fresh-rebuild.sh                   # production
#   ENV_EXT="-dev" ./admin-tools/prepare-fresh-rebuild.sh    # dev cluster

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
SERVER_IP=$(detect_server_ip "$CUR_ENV_EXT")

echo "=========================================================="
echo "⚠️  WARNING: TRUE NUKE INITIATED ('$CLUSTER' cluster)"
echo "This will back up your certificates to this machine and"
echo "WIPE ALL DATA from the persistent volume on $SERVER_IP."
echo "=========================================================="
read -p "Are you absolutely sure? (Type YES to continue): " confirm
if [ "$confirm" != "YES" ]; then
  echo "Aborting."
  exit 1
fi

echo ""
echo "1. Backing up Let's Encrypt certificates to this machine..."
ENV_EXT="$CUR_ENV_EXT" "$SCRIPT_DIR/backup-certs-to-laptop.sh"

echo ""
echo "2. Stopping K3s and unmounting volumes to release file locks..."
ssh $SSH_OPTS root@$SERVER_IP << 'EOF'
/usr/local/bin/k3s-killall.sh || true
for mount in $(awk '{print $2}' /proc/mounts | grep '^/mnt/smallworlds-data/k3s' | sort -r); do
    umount -l "$mount" || true
done
EOF

echo "3. Wiping all application data (Immich, Nextcloud, Garage, and K3s state)..."
ssh $SSH_OPTS root@$SERVER_IP "cd /mnt/smallworlds-data && find . -mindepth 1 -maxdepth 1 -exec rm -rf {} +"

echo ""
echo "✅ Preparation complete!"
echo "Your certificates are safely stored under ~/.smallworlds/cert-backups/$CLUSTER/"
echo "All other data has been destroyed."
echo ""
echo "You may now safely run:"
echo "  cd infrastructure/terraform"
echo "  terraform destroy -target=hcloud_server.smallworlds_pilot_node"
echo "  terraform apply"
echo "  cd ../.. && ./admin-tools/restore-certs-from-laptop.sh"
echo ""
echo "The restore script waits for the new cluster's API to come up and injects"
echo "the certificates before cert-manager re-issues them."
