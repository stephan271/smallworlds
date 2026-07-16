#!/bin/bash
set -e

# Backs up all Let's Encrypt certificates (and the ACME account key) from the
# live cluster to your local machine, so a cluster rebuilt from scratch can
# reuse them instead of re-issuing — avoiding Let's Encrypt rate limits.
#
# Backups are stored per environment (production vs .dev cluster, selected via
# the terraform `env_ext` variable / ENV_EXT override — see lib/cluster-env.sh):
#   ~/.smallworlds/cert-backups/<production|dev>/<timestamp>/certs-backup.yaml
#
# Usage:
#   ./admin-tools/backup-certs-to-laptop.sh [backup-root]   # production
#   ENV_EXT=".dev" ./admin-tools/backup-certs-to-laptop.sh  # dev cluster
#
# Kubeconfig: uses $KUBECONFIG if set; otherwise fetches a fresh one from the
# server over SSH (locally cached kubeconfigs go stale after every rebuild).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/cluster-env.sh"

CUR_ENV_EXT=$(detect_env_ext)
CLUSTER=$(cluster_label "$CUR_ENV_EXT")
BACKUP_ROOT="${1:-$HOME/.smallworlds/cert-backups/$CLUSTER}"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
BACKUP_DIR="$BACKUP_ROOT/$TIMESTAMP"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Backing up certificates from the '$CLUSTER' cluster..."

if [ -z "$KUBECONFIG" ]; then
  SERVER_IP=$(detect_server_ip "$CUR_ENV_EXT")
  echo "1. Fetching kubeconfig from $SERVER_IP (KUBECONFIG not set)..."
  export KUBECONFIG="$WORK_DIR/kubeconfig.yaml"
  fetch_kubeconfig "$SERVER_IP" "$KUBECONFIG" \
    || { echo "Error: could not fetch kubeconfig from root@$SERVER_IP."; exit 1; }
else
  echo "1. Using kubeconfig from \$KUBECONFIG ($KUBECONFIG)..."
fi

echo "2. Fetching Let's Encrypt ACME account key and TLS secrets from cluster..."
kubectl get secret letsencrypt-prod -n cert-manager -o json > "$WORK_DIR/letsencrypt-prod.json" \
  || echo "Warning: letsencrypt-prod secret not found."
kubectl get secret -A --field-selector type=kubernetes.io/tls -o json > "$WORK_DIR/tls-certs.json" \
  || { echo "Error: could not fetch TLS secrets — is the cluster reachable?"; exit 1; }

echo "3. Filtering to letsencrypt-prod certificates and cleaning metadata..."
mkdir -p "$BACKUP_DIR"
WORK_DIR="$WORK_DIR" BACKUP_DIR="$BACKUP_DIR" python3 - << 'PYEOF'
import json, os

work_dir = os.environ["WORK_DIR"]
backup_dir = os.environ["BACKUP_DIR"]

def load(path):
    try:
        with open(path) as f:
            return json.load(f)
    except Exception:
        return {"items": []}

le_data = load(f"{work_dir}/letsencrypt-prod.json")
tls_data = load(f"{work_dir}/tls-certs.json")

items = []
if "items" in le_data:
    items.extend(le_data["items"])
elif "metadata" in le_data:
    items.append(le_data)

def is_letsencrypt_prod(item):
    ann = item.get("metadata", {}).get("annotations", {})
    issuer = ann.get("cert-manager.io/cluster-issuer-name") or ann.get("cert-manager.io/issuer-name")
    return issuer == "letsencrypt-prod"

for item in tls_data.get("items", []):
    if is_letsencrypt_prod(item):
        items.append(item)

for item in items:
    for key in ["creationTimestamp", "resourceVersion", "uid",
                "ownerReferences", "generation", "managedFields"]:
        item.get("metadata", {}).pop(key, None)
    item.pop("status", None)

merged = {"apiVersion": "v1", "kind": "List", "items": items}
with open(f"{backup_dir}/certs-backup.yaml", "w") as f:
    json.dump(merged, f, indent=2)

print(f"   Saved {len(items)} secrets:")
for item in items:
    md = item.get("metadata", {})
    print(f"   - {md.get('namespace', '?')}/{md.get('name', '?')}")
PYEOF

echo "4. Checking certificate expiry dates..."
BACKUP_DIR="$BACKUP_DIR" python3 - << 'PYEOF'
import json, base64, os, subprocess

backup_dir = os.environ["BACKUP_DIR"]
with open(f"{backup_dir}/certs-backup.yaml") as f:
    data = json.load(f)

for item in data["items"]:
    crt = item.get("data", {}).get("tls.crt")
    if not crt:
        continue
    md = item["metadata"]
    pem = base64.b64decode(crt)
    result = subprocess.run(["openssl", "x509", "-noout", "-enddate"],
                            input=pem, capture_output=True)
    end = result.stdout.decode().strip().replace("notAfter=", "")
    print(f"   - {md.get('namespace')}/{md.get('name')} expires: {end}")
PYEOF

# Keep a stable pointer to the most recent backup
ln -sfn "$BACKUP_DIR" "$BACKUP_ROOT/latest"

echo ""
echo "✅ Backup complete: $BACKUP_DIR/certs-backup.yaml"
echo "   ('latest' symlink updated: $BACKUP_ROOT/latest)"
echo ""
echo "⚠️  Let's Encrypt certificates are valid for 90 days. A backup older than"
echo "   the remaining lifetime is useless — cert-manager will simply re-issue."
echo "   Re-run this script periodically (e.g. after each cert-manager renewal)."
echo ""
echo "To restore into a fresh '$CLUSTER' cluster after terraform apply:"
echo "  ./admin-tools/restore-certs-from-laptop.sh"
