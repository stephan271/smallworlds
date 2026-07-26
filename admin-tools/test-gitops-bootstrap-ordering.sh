#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "$0")/.." && pwd)
apps_directory="$repository_root/infrastructure/kubernetes/apps"

assert_contains() {
    local file=$1
    local expected=$2
    if ! grep -Fq -- "$expected" "$file"; then
        echo "Expected $file to contain: $expected" >&2
        exit 1
    fi
}

# A fresh cluster must establish each CRD/webhook provider before its consumer
# Application is allowed to sync. Consumers also retain retries for slow cold
# image pulls and API discovery propagation.
assert_contains "$apps_directory/cert-manager.yaml" 'argocd.argoproj.io/sync-wave: "-10"'
assert_contains "$apps_directory/cert-manager-webhook-hetzner.yaml" 'argocd.argoproj.io/sync-wave: "-9"'
assert_contains "$apps_directory/cert-manager-webhook-hetzner.yaml" 'retry:'

# A ClusterIssuer must never be dropped into k3s's auto-applied manifest
# directory. k3s retries that directory forever on failure, and a ClusterIssuer
# cannot apply until cert-manager's CRDs exist — which is only after Argo CD has
# synced. The result was an apply-fail-retry loop writing events into etcd every
# few seconds for as long as the cluster took to converge. Both bootstrap paths
# wait for the CRD and apply it once instead.
local_bootstrap="$repository_root/infrastructure/local/bootstrap-local-node.sh"
cloud_init="$repository_root/infrastructure/cloud-init/k3s-node.yaml.tpl"
for bootstrap in "$local_bootstrap" "$cloud_init"; do
    if grep -Fq -- 'server/manifests/letsencrypt-prod.yaml' "$bootstrap"; then
        echo "Expected $bootstrap not to auto-apply a ClusterIssuer" >&2
        exit 1
    fi
    assert_contains "$bootstrap" 'crd/clusterissuers.cert-manager.io'
    assert_contains "$bootstrap" 'kubectl get crd clusterissuers.cert-manager.io'
done

assert_contains "$apps_directory/kube-prometheus-stack.yaml" 'argocd.argoproj.io/sync-wave: "0"'
assert_contains "$apps_directory/trivy-operator.yaml" 'argocd.argoproj.io/sync-wave: "1"'
assert_contains "$apps_directory/trivy-operator.yaml" 'retry:'

echo "GitOps bootstrap dependency ordering is valid."
