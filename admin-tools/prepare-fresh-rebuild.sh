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
kubectl get secret letsencrypt-prod -n cert-manager -o json > /tmp/letsencrypt-prod.json || echo "Warning: letsencrypt-prod secret not found."
kubectl get secret -A --field-selector type=kubernetes.io/tls -o json > /tmp/tls-certs.json

echo "2. Cleaning up backup payload..."
python3 -c '
import sys, json

try:
    with open("/tmp/letsencrypt-prod.json", "r") as f:
        le_data = json.load(f)
except Exception:
    le_data = {"items": []}

try:
    with open("/tmp/tls-certs.json", "r") as f:
        tls_data = json.load(f)
except Exception:
    tls_data = {"items": []}

items = []
if "items" in le_data:
    items.extend(le_data["items"])
elif "metadata" in le_data:
    items.append(le_data)

if "items" in tls_data:
    items.extend(tls_data["items"])
elif "metadata" in tls_data:
    items.append(tls_data)

for item in items:
    for key in ["creationTimestamp", "resourceVersion", "uid", "ownerReferences", "generation"]:
        item.get("metadata", {}).pop(key, None)
    item.pop("status", None)

merged = {"apiVersion": "v1", "kind": "List", "items": items}
with open("/tmp/certs-backup.yaml", "w") as f:
    json.dump(merged, f, indent=2)
'

echo "3. Transferring backup to the server's persistent volume..."
scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null /tmp/certs-backup.yaml root@$SERVER_IP:/mnt/smallworlds-data/certs-backup.yaml

echo "4. Stopping K3s and unmounting volumes to release file locks..."
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@$SERVER_IP "/usr/local/bin/k3s-killall.sh || true"

echo "5. Wiping all application data (Immich, Nextcloud, Garage, and K3s state)..."
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@$SERVER_IP "cd /mnt/smallworlds-data && find . -mindepth 1 -maxdepth 1 ! -name 'certs-backup.yaml' -exec rm -rf {} +"

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
