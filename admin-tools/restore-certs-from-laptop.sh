#!/bin/bash
set -e

# Restores a local certificate backup (made by backup-certs-to-laptop.sh) into
# a freshly built cluster, so cert-manager adopts the existing Let's Encrypt
# certificates instead of re-issuing them.
#
# Run this right after `terraform apply`: it waits for the new server's k3s to
# come up, fetches a fresh kubeconfig over SSH, and applies the secrets before
# ArgoCD has deployed cert-manager — so no new Let's Encrypt order is placed.
# If no backup exists for this environment it exits cleanly (certs will simply
# be issued fresh).
#
# Environment (production vs .dev) is auto-detected — see lib/cluster-env.sh.
#
# Usage:
#   ./admin-tools/restore-certs-from-laptop.sh [backup-file]   # production
#   ENV_EXT=".dev" ./admin-tools/restore-certs-from-laptop.sh  # dev cluster

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
BACKUP_FILE="${1:-$HOME/.smallworlds/cert-backups/$CLUSTER/latest/certs-backup.yaml}"

if [ ! -f "$BACKUP_FILE" ]; then
  echo "No certificate backup found for the '$CLUSTER' cluster at:"
  echo "  $BACKUP_FILE"
  echo "Skipping restore — cert-manager will issue fresh certificates."
  exit 0
fi

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Restoring '$CLUSTER' certificates from: $BACKUP_FILE"

echo ""
echo "1. Checking backup for expired certificates..."
BACKUP_FILE="$BACKUP_FILE" python3 - << 'PYEOF'
import json, base64, os, subprocess, sys

with open(os.environ["BACKUP_FILE"]) as f:
    data = json.load(f)

expired = 0
for item in data.get("items", []):
    crt = item.get("data", {}).get("tls.crt")
    if not crt:
        continue
    md = item["metadata"]
    pem = base64.b64decode(crt)
    result = subprocess.run(["openssl", "x509", "-noout", "-checkend", "0"],
                            input=pem, capture_output=True)
    if result.returncode != 0:
        expired += 1
        print(f"   ⚠️  {md.get('namespace')}/{md.get('name')} has EXPIRED — "
              "cert-manager will re-issue it after restore.")
if expired == 0:
    print("   All certificates still valid.")
PYEOF

if [ -z "$KUBECONFIG" ]; then
  SERVER_IP=$(detect_server_ip "$CUR_ENV_EXT")
  echo ""
  echo "2. Waiting for k3s on $SERVER_IP (cloud-init may still be running)..."
  export KUBECONFIG="$WORK_DIR/kubeconfig.yaml"
  for i in $(seq 1 60); do
    if fetch_kubeconfig "$SERVER_IP" "$KUBECONFIG" \
       && kubectl get nodes 2>/dev/null | grep -q " Ready"; then
      break
    fi
    if [ "$i" = "60" ]; then
      echo "Error: cluster at $SERVER_IP did not become ready within 15 minutes."
      exit 1
    fi
    sleep 15
  done
  echo "   Cluster is up."
else
  echo ""
  echo "2. Using kubeconfig from \$KUBECONFIG ($KUBECONFIG)..."
fi

echo ""
echo "3. Creating target namespaces (they may not exist yet on a fresh cluster)..."
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
echo "4. Applying certificate secrets..."
kubectl apply -f "$BACKUP_FILE"

echo ""
echo "✅ Certificates restored."
echo ""
echo "cert-manager will adopt these secrets when the Certificate resources are"
echo "created by ArgoCD — as long as each cert is still valid and matches its"
echo "dnsNames, no new Let's Encrypt order is placed. Verify later with:"
echo "  kubectl get certificate -A"
echo "(READY=True with no recent CertificateRequests means adoption worked.)"
