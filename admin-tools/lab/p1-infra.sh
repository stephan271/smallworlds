#!/usr/bin/env bash
# P1 — infrastructure tier of the backup lab.
#
# Replays what ArgoCD would do for waves -10 and -5, using the exact chart
# versions and values from infrastructure/kubernetes/apps/{cloudnative-pg,garage}.yaml.
# No ArgoCD: helm directly, so there is one less moving part between a
# manifest and the cluster.
set -euo pipefail

REPO_ROOT="${REPO_ROOT:-/home/egli/development/smallworlds}"
export KUBECONFIG="${KUBECONFIG:-$HOME/.smallworlds/kubeconfigs/lab.yaml}"

say() { printf '\033[0;36m==> %s\033[0m\n' "$*"; }

# ------------------------------------------------------------------
# 1. static-local StorageClass + the two static PVs
# ------------------------------------------------------------------
say "Applying persistent-storage (static-local SC + garage/immich PVs)"
kubectl apply -f "$REPO_ROOT/infrastructure/kubernetes/apps/persistent-storage.yaml"

# ------------------------------------------------------------------
# 2. Garage's RPC/admin credential
#
# apps/garage.yaml sets secret.create=false and names garage-auth-secret, so
# something outside the chart must provide it. In production that is the
# operator secrets manifest; test-pr-locally.sh:206 does the same thing for
# staging, with the same two keys.
# ------------------------------------------------------------------
say "Creating namespaces and garage-auth-secret"
kubectl create namespace garage-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace cnpg-system   --dry-run=client -o yaml | kubectl apply -f -

if ! kubectl get secret garage-auth-secret -n garage-system >/dev/null 2>&1; then
    kubectl create secret generic garage-auth-secret -n garage-system \
        --from-literal=rpcSecret="$(openssl rand -hex 32)" \
        --from-literal=adminToken="$(openssl rand -hex 32)"
else
    echo "    garage-auth-secret already exists, keeping it"
fi

# ------------------------------------------------------------------
# 3. CloudNativePG operator — chart 0.29.0, values from apps/cloudnative-pg.yaml
# ------------------------------------------------------------------
say "Installing cloudnative-pg 0.29.0"
helm repo add cnpg https://cloudnative-pg.github.io/charts >/dev/null 2>&1 || true
helm repo add garage https://datahub-local.github.io/garage-helm >/dev/null 2>&1 || true
helm repo update cnpg garage >/dev/null

helm upgrade --install cloudnative-pg cnpg/cloudnative-pg \
    --version 0.29.0 --namespace cnpg-system \
    --set config.ENABLE_METRICS=true \
    --wait --timeout 5m

# ------------------------------------------------------------------
# 4. Garage — chart 0.7.1, values copied verbatim from apps/garage.yaml
# ------------------------------------------------------------------
say "Installing garage 0.7.1"
cat > /tmp/lab-garage-values.yaml <<'VALUES'
deployment:
  replicaCount: 1
garage:
  replicationFactor: "1"
  secret:
    create: false
    name: "garage-auth-secret"
persistence:
  enabled: true
  # NOTE: apps/garage.yaml uses `persistence.{size,storageClass}`, which are
  # NOT keys in chart 0.7.1. Helm ignores them silently, so upstream Garage
  # runs on a 1Gi local-path claim and garage-data-pv is never bound.
  # The real schema is per-volume:
  meta:
    size: 100Mi          # low latency wanted; default local-path (NVMe)
  data:
    storageClass: static-local
    size: 120Gi          # binds garage-data-pv -> /mnt/smallworlds-data/garage
service:
  s3:
    type: ClusterIP
    port: 3900
  api:
    type: ClusterIP
    port: 3901
  web:
    type: ClusterIP
    port: 3902
ingress:
  enabled: false
VALUES

helm upgrade --install garage garage/garage \
    --version 0.7.1 --namespace garage-system \
    -f /tmp/lab-garage-values.yaml \
    --wait --timeout 5m

# ------------------------------------------------------------------
# 5. Garage layout — same commands the garage-init-job base runs, but that
#    job is an ArgoCD Sync hook and we have no ArgoCD. Idempotent.
# ------------------------------------------------------------------
say "Applying the Garage layout"
POD=$(kubectl get pods -n garage-system -l app.kubernetes.io/name=garage -o name | head -1)
if ! kubectl exec -n garage-system "$POD" -- /garage status 2>/dev/null | grep -q 'dc1'; then
    NODES=$(kubectl exec -n garage-system "$POD" -- /garage status 2>/dev/null \
            | grep -E '^[0-9a-f]{16}' | awk '{print $1}')
    for N in $NODES; do
        kubectl exec -n garage-system "$POD" -- /garage layout assign -z dc1 -c 100G "$N" || true
    done
    kubectl exec -n garage-system "$POD" -- /garage layout apply --version 1 || true
else
    echo "    layout already assigned"
fi

# ------------------------------------------------------------------
# 6. Verify — the payoff for using the real node name is that the
#    static-local PVs bind with no patching at all.
# ------------------------------------------------------------------
say "Result"
kubectl get sc
kubectl get pv
kubectl get pvc -A
kubectl get pods -n garage-system -n cnpg-system 2>/dev/null || true
kubectl get pods -A --field-selector=status.phase!=Running -o wide | head
