#!/bin/bash
set -e

# Restores a local certificate backup (made by backup-certs-to-laptop.sh) into
# a freshly built cluster, so cert-manager adopts the existing Let's Encrypt
# certificates instead of re-issuing them.
#
# Run this as soon as the new cluster's API is reachable — ideally right after
# `terraform apply`, before (or while) ArgoCD deploys the apps. cert-manager
# sees a valid existing tls secret for each Certificate and skips issuance.
#
# Usage:
#   export KUBECONFIG=$PWD/k3s_kubeconfig.yaml
#   ./admin-tools/restore-certs-from-laptop.sh [backup-file]
#
# Default backup file: ~/.smallworlds/cert-backups/latest/certs-backup.yaml

BACKUP_FILE="${1:-$HOME/.smallworlds/cert-backups/latest/certs-backup.yaml}"

if [ ! -f "$BACKUP_FILE" ]; then
  echo "Error: backup file not found: $BACKUP_FILE"
  echo "Run ./admin-tools/backup-certs-to-laptop.sh against the old cluster first."
  exit 1
fi

echo "Restoring certificates from: $BACKUP_FILE"
echo ""

echo "1. Creating target namespaces (they may not exist yet on a fresh cluster)..."
NAMESPACES=$(BACKUP_FILE="$BACKUP_FILE" python3 -c '
import json, os
with open(os.environ["BACKUP_FILE"]) as f:
    data = json.load(f)
namespaces = sorted({item["metadata"]["namespace"]
                     for item in data.get("items", [])
                     if item.get("metadata", {}).get("namespace")})
print("\n".join(namespaces))
')
for ns in $NAMESPACES; do
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
done

echo ""
echo "2. Applying certificate secrets..."
kubectl apply -f "$BACKUP_FILE"

echo ""
echo "✅ Certificates restored."
echo ""
echo "cert-manager will adopt these secrets when the Certificate resources are"
echo "created by ArgoCD — as long as each cert is still valid and matches its"
echo "dnsNames, no new Let's Encrypt order is placed. Verify later with:"
echo "  kubectl get certificate -A"
echo "(READY=True with no recent CertificateRequests means adoption worked.)"
