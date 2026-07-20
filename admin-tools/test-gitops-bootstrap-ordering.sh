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

assert_contains "$apps_directory/kube-prometheus-stack.yaml" 'argocd.argoproj.io/sync-wave: "0"'
assert_contains "$apps_directory/trivy-operator.yaml" 'argocd.argoproj.io/sync-wave: "1"'
assert_contains "$apps_directory/trivy-operator.yaml" 'retry:'

echo "GitOps bootstrap dependency ordering is valid."
