#!/bin/bash
set -e

# Automatically figure out the server IP from Terraform or default to DNS
SERVER_IP=$(terraform -chdir=infrastructure/terraform output -raw server_ipv4 2>/dev/null || echo "identity.smallworlds.network")

echo "=========================================================="
echo "⚠️  WARNING: TRUE NUKE INITIATED"
echo "This will back up your certificates and WIPE ALL DATA"
echo "from your persistent volume on $SERVER_IP."
echo "=========================================================="
read -p "Are you absolutely sure? (Type YES to continue): " confirm
if [ "$confirm" != "YES" ]; then
  echo "Aborting."
  exit 1
fi

echo ""
echo "1. Backing up Let's Encrypt certificates from Kubernetes..."
kubectl get secret letsencrypt-prod -n cert-manager -o yaml > /tmp/letsencrypt-prod.yaml || echo "Warning: letsencrypt-prod secret not found."
kubectl get secret -A --field-selector type=kubernetes.io/tls -o yaml > /tmp/tls-certs.yaml

cat /tmp/letsencrypt-prod.yaml /tmp/tls-certs.yaml > /tmp/certs-backup.yaml

echo "2. Cleaning up backup payload..."
yq eval 'del(.items[].metadata.creationTimestamp, .items[].metadata.resourceVersion, .items[].metadata.uid, .items[].metadata.ownerReferences, .items[].metadata.generation, .items[].status)' -i /tmp/certs-backup.yaml

echo "3. Transferring backup to the server's persistent volume..."
scp /tmp/certs-backup.yaml root@$SERVER_IP:/mnt/smallworlds-data/certs-backup.yaml

echo "4. Stopping K3s to release file locks..."
ssh root@$SERVER_IP "systemctl stop k3s || true"

echo "5. Wiping all application data (Immich, Nextcloud, Garage, and K3s state)..."
ssh root@$SERVER_IP "cd /mnt/smallworlds-data && find . -mindepth 1 -maxdepth 1 ! -name 'certs-backup.yaml' -exec rm -rf {} +"

echo ""
echo "✅ Preparation complete!"
echo "Your certificates are safely stored at /mnt/smallworlds-data/certs-backup.yaml"
echo "All other data has been destroyed."
echo ""
echo "You may now safely run:"
echo "  cd infrastructure/terraform"
echo "  terraform destroy"
echo "  terraform apply"
echo ""
echo "When the new server boots, cloud-init will automatically detect the certs-backup.yaml"
echo "file on the persistent volume and inject it into the new cluster."
