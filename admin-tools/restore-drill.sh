#!/usr/bin/env bash
# Reproducible, destructive restore drill on the disposable Hetzner staging
# cluster. Never point this at production: the exercise deliberately clears the
# Immich library, Nextcloud object bucket and both application databases.
set -euo pipefail

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
TF_DIR="$REPO_ROOT/infrastructure/terraform-staging"
RUNTIME_DIR="${RESTORE_DRILL_RUNTIME_DIR:-/tmp/smallworlds-restore-drill}"
EVIDENCE_DIR="${RESTORE_DRILL_EVIDENCE_DIR:-$REPO_ROOT/admin-tools/restore-drill/evidence}"
KUBECONFIG_PATH="$RUNTIME_DIR/kubeconfig.yaml"
SSH_KEY="$RUNTIME_DIR/staging-ssh"
STATE_JSON="$RUNTIME_DIR/app-data.json"
RUN_ID_FILE="$RUNTIME_DIR/run-id"
USER_PASSWORD_FILE="$RUNTIME_DIR/user-password"
DOMAIN="${DOMAIN:-smallworlds.network}"
APPS=(cert-manager cloudnative-pg cnpg-barman-plugin garage garage-backup traefik keycloak velero immich nextcloud)

say() { printf "${CYAN}==> %s${NC}\n" "$*"; }
pass() { printf "${GREEN}PASS: %s${NC}\n" "$*"; }
die() { printf "${RED}ERROR: %s${NC}\n" "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: ./admin-tools/restore-drill.sh {provision|deploy|exercise|destroy|all}

  provision  create a fresh Hetzner staging cluster with separate data/backup volumes
  deploy     resume/idempotently converge apps on an already provisioned drill cluster
  exercise   seed three users, create Recovery Points, erase app state, restore and verify
  destroy    delete the disposable VM, volumes, firewall and SSH key
  all        provision + exercise + destroy (KEEP_CLUSTER=1 skips final destroy)

HCLOUD_TOKEN may be exported or read from .smallworlds-cache.env. The script
refuses every kubeconfig except its own runtime kubeconfig and requires the node
name cc-staging-node-01 before performing destructive work.
EOF
}

load_hcloud_token() {
  if [[ -z "${HCLOUD_TOKEN:-}" && -f "$REPO_ROOT/.smallworlds-cache.env" ]]; then
    # shellcheck disable=SC1091
    source "$REPO_ROOT/.smallworlds-cache.env"
  fi
  [[ -n "${HCLOUD_TOKEN:-}" ]] || die "HCLOUD_TOKEN is not configured"
  export TF_VAR_hcloud_token="$HCLOUD_TOKEN"
}

ensure_runtime() {
  mkdir -p "$RUNTIME_DIR" "$EVIDENCE_DIR"
  chmod 700 "$RUNTIME_DIR"
  if [[ ! -f "$RUN_ID_FILE" ]]; then date -u +%Y%m%dT%H%M%SZ > "$RUN_ID_FILE"; fi
  if [[ ! -f "$USER_PASSWORD_FILE" ]]; then
    umask 077
    openssl rand -base64 24 | tr -d '\n' > "$USER_PASSWORD_FILE"
  fi
  if [[ ! -f "$SSH_KEY" ]]; then ssh-keygen -q -t ed25519 -N '' -f "$SSH_KEY"; fi
  export TF_VAR_ssh_public_key_path="$SSH_KEY.pub"
  export TF_VAR_separate_backup_volumes=true
  export TF_VAR_use_golden_image=false
}

k() { KUBECONFIG="$KUBECONFIG_PATH" kubectl "$@"; }

assert_disposable_cluster() {
  [[ -f "$KUBECONFIG_PATH" ]] || die "runtime kubeconfig not found; run provision first"
  local server node
  server=$(KUBECONFIG="$KUBECONFIG_PATH" kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
  [[ "$server" != *"89.167.3.140"* ]] || die "refusing the production API endpoint"
  node=$(k get nodes -o jsonpath='{.items[0].metadata.name}')
  [[ "$node" == "cc-staging-node-01" ]] || die "refusing destructive work on node '$node'"
  [[ $(k get nodes --no-headers | wc -l) -eq 1 ]] || die "restore drill requires its one-node disposable cluster"
}

wait_application() {
  local name="$1" deadline=$((SECONDS + 1800)) health sync
  while (( SECONDS < deadline )); do
    health=$(k get application "$name" -n argocd -o jsonpath='{.status.health.status}' 2>/dev/null || true)
    sync=$(k get application "$name" -n argocd -o jsonpath='{.status.sync.status}' 2>/dev/null || true)
    # Some pinned charts have known post-render drift (for example Garage's
    # StatefulSet defaults), so Healthy + a completed comparison is the useful
    # gate. Unknown is never accepted: it is the repo-server startup race.
    if [[ "$health" == Healthy && "$sync" != Unknown && -n "$sync" ]]; then return 0; fi
    printf '  %-24s health=%-12s sync=%s\n' "$name" "${health:-pending}" "${sync:-pending}"
    sleep 10
  done
  k get application "$name" -n argocd -o yaml >&2 || true
  die "Argo application '$name' did not become Healthy and Synced"
}

wait_job() {
  local namespace="$1" name="$2" timeout="${3:-900s}"
  if ! k wait -n "$namespace" --for=condition=complete "job/$name" --timeout="$timeout"; then
    k logs -n "$namespace" "job/$name" --all-containers --tail=200 >&2 || true
    k describe -n "$namespace" "job/$name" >&2 || true
    return 1
  fi
}

job_from_cron() {
  local namespace="$1" cron="$2" name="$3"
  if [[ $(k get job -n "$namespace" "$name" -o jsonpath='{.status.succeeded}' 2>/dev/null || true) == 1 ]]; then
    printf '  checkpoint: job/%s already completed\n' "$name"
    return 0
  fi
  k delete job -n "$namespace" "$name" --ignore-not-found >/dev/null
  k create job -n "$namespace" --from="cronjob/$cron" "$name" >/dev/null
  wait_job "$namespace" "$name" 1800s
  k logs -n "$namespace" "job/$name" --all-containers --tail=100
}

wait_phase() {
  local kind="$1" namespace="$2" name="$3" wanted="$4" timeout="${5:-1800}"
  local deadline=$((SECONDS + timeout)) phase
  while (( SECONDS < deadline )); do
    phase=$(k get "$kind" "$name" -n "$namespace" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [[ "$phase" == "$wanted" ]] && return 0
    case "$phase" in Failed|PartiallyFailed|ValidationFailed) die "$kind/$namespace/$name entered phase $phase";; esac
    printf '  %s/%s phase=%s\n' "$kind" "$name" "${phase:-pending}"
    sleep 10
  done
  die "$kind/$namespace/$name did not reach $wanted"
}

provision() {
  load_hcloud_token
  ensure_runtime
  say "Removing any previous disposable staging resources"
  terraform -chdir="$TF_DIR" init -input=false
  terraform -chdir="$TF_DIR" destroy -auto-approve -input=false

  say "Provisioning cx43 plus separate operational and backup volumes"
  terraform -chdir="$TF_DIR" apply -auto-approve -input=false
  local server_ip
  server_ip=$(terraform -chdir="$TF_DIR" output -raw server_ipv4)
  printf '%s\n' "$server_ip" > "$RUNTIME_DIR/server-ip"

  say "Waiting for k3s and retrieving its dedicated kubeconfig"
  timeout 600 bash -c "until ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i '$SSH_KEY' root@'$server_ip' 'test -s /root/k3s.yaml' 2>/dev/null; do sleep 10; done"
  scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$SSH_KEY" \
    "root@$server_ip:/root/k3s.yaml" "$KUBECONFIG_PATH"
  chmod 600 "$KUBECONFIG_PATH"
  assert_disposable_cluster

  say "Applying namespaces, storage and synthetic bootstrap secrets"
  k apply -f "$REPO_ROOT/infrastructure/kubernetes/namespaces.yaml"
  for namespace in garage-system garage-backup-system cnpg-system traefik-system cert-manager; do
    k create namespace "$namespace" --dry-run=client -o yaml | k apply -f -
  done
  k apply -f "$REPO_ROOT/infrastructure/kubernetes/apps/priorityclasses.yaml"
  sed 's/cc-pilot-node-01/cc-staging-node-01/g' \
    "$REPO_ROOT/infrastructure/kubernetes/apps/persistent-storage.yaml" \
    > "$RUNTIME_DIR/persistent-storage.yaml"
  k apply -f "$RUNTIME_DIR/persistent-storage.yaml"
  local garage_rpc garage_admin backup_rpc backup_admin
  garage_rpc=$(openssl rand -hex 32); garage_admin=$(openssl rand -hex 32)
  backup_rpc=$(openssl rand -hex 32); backup_admin=$(openssl rand -hex 32)
  k create secret generic garage-auth-secret -n garage-system \
    --from-literal=rpcSecret="$garage_rpc" --from-literal=adminToken="$garage_admin" \
    --dry-run=client -o yaml | k apply -f -
  k create secret generic garage-auth-secret -n garage-backup-system \
    --from-literal=rpcSecret="$backup_rpc" --from-literal=adminToken="$backup_admin" \
    --dry-run=client -o yaml | k apply -f -
  k create secret generic keycloak-admin-creds -n keycloak \
    --from-literal=admin-password=restore-drill-admin \
    --from-literal=bulk-invite-secret=restore-drill-invite \
    --dry-run=client -o yaml | k apply -f -
  k create configmap smallworlds-global-config -n default \
    --from-literal=ADMIN_EMAIL=restore-admin@restore.invalid \
    --from-literal=DOMAIN="$DOMAIN" --dry-run=client -o yaml | k apply -f -

  deploy
}

deploy() {
  ensure_runtime
  assert_disposable_cluster
  say "Deploying the production-pinned infrastructure and two applications"
  k rollout status deployment/argocd-repo-server -n argocd --timeout=300s
  for app in "${APPS[@]}"; do
    k apply -f "$REPO_ROOT/infrastructure/kubernetes/apps/$app.yaml"
  done
  # Applications can be created in the short interval where the Service has an
  # address but repo-server does not accept gRPC yet. The resulting ComparisonError
  # otherwise sits until Argo's next periodic refresh.
  k annotate applications -n argocd --all argocd.argoproj.io/refresh=hard --overwrite
  for app in "${APPS[@]}"; do wait_application "$app"; done

  # HEAD does not include the worktree's new Immich central mirror yet. Apply
  # exactly that declared CronJob locally; all future committed runs receive it
  # through the tenant kustomization and this apply is idempotent.
  k apply -n immich -f "$REPO_ROOT/infrastructure/kubernetes/bases/pv-backup-job/pv-backup-job.yaml"
  k patch cronjob pv-backup -n immich --type=json -p='[
    {"op":"replace","path":"/spec/schedule","value":"30 0 * * *"},
    {"op":"replace","path":"/spec/jobTemplate/spec/template/spec/volumes/0/persistentVolumeClaim/claimName","value":"immich-library-pvc"},
    {"op":"replace","path":"/spec/jobTemplate/spec/template/spec/containers/0/env/0/value","value":"immich/pv-backup/library"}
  ]'

  local data_volume backup_volume
  data_volume=$(terraform -chdir="$TF_DIR" output -raw data_volume_id)
  backup_volume=$(terraform -chdir="$TF_DIR" output -raw backup_volume_id)
  [[ -n "$data_volume" && -n "$backup_volume" && "$data_volume" != "$backup_volume" ]] \
    || die "operational and backup volumes are not distinct"
  pass "fresh disposable cluster is healthy; data volume $data_volume, backup volume $backup_volume"
}

start_forwards() {
  local nc_port
  nc_port=$(k get svc nextcloud -n nextcloud -o jsonpath='{.spec.ports[0].port}')
  k port-forward -n nextcloud svc/nextcloud "18081:$nc_port" >"$RUNTIME_DIR/nextcloud-port-forward.log" 2>&1 &
  NC_PF_PID=$!
  k port-forward -n immich svc/immich-server 18082:2283 >"$RUNTIME_DIR/immich-port-forward.log" 2>&1 &
  IMMICH_PF_PID=$!
  for _ in $(seq 1 60); do
    if curl -sf http://127.0.0.1:18082/api/server/ping >/dev/null 2>&1 \
       && curl -s -o /dev/null http://127.0.0.1:18081/status.php; then return 0; fi
    sleep 1
  done
  die "application port-forwards did not become ready"
}

stop_forwards() {
  [[ -z "${NC_PF_PID:-}" ]] || kill "$NC_PF_PID" 2>/dev/null || true
  [[ -z "${IMMICH_PF_PID:-}" ]] || kill "$IMMICH_PF_PID" 2>/dev/null || true
  NC_PF_PID=""; IMMICH_PF_PID=""
}

app_data() {
  local action="$1" user_password immich_email immich_password nc_user nc_password
  user_password=$(<"$USER_PASSWORD_FILE")
  immich_email=$(k get secret immich-admin-creds -n immich -o jsonpath='{.data.email}' | base64 -d)
  immich_password=$(k get secret immich-admin-creds -n immich -o jsonpath='{.data.password}' | base64 -d)
  nc_user=$(k get secret nextcloud-admin-creds -n nextcloud -o jsonpath='{.data.nextcloud-username}' | base64 -d)
  nc_password=$(k get secret nextcloud-admin-creds -n nextcloud -o jsonpath='{.data.nextcloud-password}' | base64 -d)
  start_forwards
  python3 "$REPO_ROOT/admin-tools/restore-drill/app-data.py" "$action" \
    --state "$STATE_JSON" --user-password "$user_password" \
    --immich-admin-email "$immich_email" --immich-admin-password "$immich_password" \
    --nextcloud-admin-user "$nc_user" --nextcloud-admin-password "$nc_password"
  stop_forwards
}

wait_source_apps() {
  say "Waiting for both source databases and application endpoints"
  # Makes the exercise resumable when a pre-backup failure occurred after the
  # writers were quiesced. This is safe only because assert_disposable_cluster
  # has already bounded the target.
  k scale deployment -n immich immich-server immich-machine-learning --replicas=1 >/dev/null
  k scale deployment -n nextcloud nextcloud --replicas=1 >/dev/null
  k wait cluster/database -n immich --for=condition=Ready --timeout=1800s
  k wait cluster/database -n nextcloud --for=condition=Ready --timeout=1800s
  k rollout status deployment/immich-server -n immich --timeout=1800s
  k rollout status deployment/nextcloud -n nextcloud --timeout=1800s
  # The hook creates/configures the local Immich administrator only after the
  # server is ready. Argo deletes it on success, so wait for the credential and
  # then prove login indirectly through app_data rather than trusting hook state.
  for _ in $(seq 1 120); do
    k get secret immich-admin-creds -n immich >/dev/null 2>&1 \
      && k get secret nextcloud-admin-creds -n nextcloud >/dev/null 2>&1 \
      && break
    sleep 5
  done
  k get secret immich-admin-creds -n immich >/dev/null 2>&1 \
    || die "Immich administrator credentials were not initialized"
  k get secret nextcloud-admin-creds -n nextcloud >/dev/null 2>&1 \
    || die "Nextcloud administrator credentials were not initialized"
  # The credential Secret precedes the account-creation hook. Waiting only for
  # the Secret races the first login on a fresh cluster.
  for _ in $(seq 1 120); do
    if k get job immich-admin-init -n immich >/dev/null 2>&1; then
      wait_job immich immich-admin-init 600s
      return 0
    fi
    # Argo removes a successful hook quickly; absence after a successful app
    # operation therefore also means initialization is complete.
    [[ $(k get application immich -n argocd -o jsonpath='{.status.operationState.phase}' 2>/dev/null || true) == Succeeded ]] \
      && return 0
    sleep 5
  done
  die "Immich administrator account hook did not complete"
}

pause_gitops_and_apps() {
  say "Pausing reconciliation and quiescing application writers"
  for app in immich nextcloud; do
    k patch application "$app" -n argocd --type=json \
      -p='[{"op":"remove","path":"/spec/syncPolicy/automated"}]' 2>/dev/null || true
  done
  k scale deployment -n immich immich-server immich-machine-learning --replicas=0
  k scale deployment -n nextcloud nextcloud --replicas=0
  k wait -n immich --for=delete pod -l app.kubernetes.io/name=server --timeout=300s || true
  k wait -n nextcloud --for=delete pod -l app.kubernetes.io/name=nextcloud --timeout=300s || true
}

create_recovery_points() {
  local run_id="$1"
  say "Creating explicit database, file and cluster-state Recovery Points"
  for namespace in immich nextcloud; do
    k apply -f - <<EOF
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata:
  name: restore-drill-$run_id
  namespace: $namespace
spec:
  method: plugin
  pluginConfiguration:
    name: barman-cloud.cloudnative-pg.io
  cluster:
    name: database
EOF
  done
  wait_phase backups.postgresql.cnpg.io immich "restore-drill-$run_id" completed
  wait_phase backups.postgresql.cnpg.io nextcloud "restore-drill-$run_id" completed

  job_from_cron immich pv-backup "restore-drill-files-$run_id"
  job_from_cron nextcloud pv-backup "restore-drill-html-$run_id"
  job_from_cron nextcloud nextcloud-files-backup "restore-drill-objects-$run_id"

  k apply -f - <<EOF
apiVersion: velero.io/v1
kind: Backup
metadata:
  name: restore-drill-$run_id
  namespace: velero
spec:
  includedNamespaces: [immich, nextcloud]
  includedResources: [secrets]
  snapshotVolumes: false
  ttl: 168h0m0s
EOF
  wait_phase backups.velero.io velero "restore-drill-$run_id" Completed
  pass "Recovery Points completed for both databases, both file stores and runtime secrets"
}

wipe_primary_data() {
  local run_id="$1"
  say "Simulating loss of both applications' operational state"
  for namespace in immich nextcloud; do
    k delete cluster database -n "$namespace" --wait=true --timeout=600s
    k delete pvc -n "$namespace" -l cnpg.io/cluster=database --ignore-not-found --wait=true
    k delete secret database-app -n "$namespace" --ignore-not-found
  done

  k apply -f - <<'EOF'
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-wipe-library, namespace: immich}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: wipe
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c, 'find /data -mindepth 1 -delete']
          volumeMounts: [{name: data, mountPath: /data}]
      volumes:
        - name: data
          persistentVolumeClaim: {claimName: immich-library-pvc}
---
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-wipe-html, namespace: nextcloud}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: wipe
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c, 'find /data -mindepth 1 -delete']
          volumeMounts: [{name: data, mountPath: /data}]
      volumes:
        - name: data
          persistentVolumeClaim: {claimName: nextcloud-nextcloud}
---
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-wipe-objects, namespace: nextcloud}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: wipe
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c]
          args: ['rclone delete live:nextcloud -v --rmdirs']
          env:
            - {name: RCLONE_CONFIG_LIVE_TYPE, value: s3}
            - {name: RCLONE_CONFIG_LIVE_PROVIDER, value: Other}
            - {name: RCLONE_CONFIG_LIVE_ENDPOINT, value: 'http://garage.garage-system.svc.cluster.local:3900'}
            - {name: RCLONE_CONFIG_LIVE_REGION, value: garage}
            - {name: RCLONE_CONFIG_LIVE_FORCE_PATH_STYLE, value: 'true'}
            - name: RCLONE_CONFIG_LIVE_ACCESS_KEY_ID
              valueFrom: {secretKeyRef: {name: garage-secret, key: accessKey}}
            - name: RCLONE_CONFIG_LIVE_SECRET_ACCESS_KEY
              valueFrom: {secretKeyRef: {name: garage-secret, key: secretKey}}
EOF
  wait_job immich restore-drill-wipe-library
  wait_job nextcloud restore-drill-wipe-html
  wait_job nextcloud restore-drill-wipe-objects
  pass "operational databases, Immich originals, Nextcloud HTML and Nextcloud objects were removed"
}

restore_secrets() {
  local run_id="$1"
  say "Restoring runtime-generated database credentials from Velero"
  k apply -f - <<EOF
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: restore-drill-$run_id
  namespace: velero
spec:
  backupName: restore-drill-$run_id
  includedNamespaces: [immich, nextcloud]
  includedResources: [secrets]
  existingResourcePolicy: update
  restorePVs: false
EOF
  wait_phase restores.velero.io velero "restore-drill-$run_id" Completed
  k get secret database-app -n immich >/dev/null
  k get secret database-app -n nextcloud >/dev/null
}

restore_files() {
  say "Restoring all-user file data from garage-backup"
  k apply -f - <<'EOF'
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-library, namespace: immich}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: restore
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c]
          args: ['rclone sync backup:immich/pv-backup/library /data -v --links --fast-list --transfers 16 --checkers 32 --stats-one-line']
          env:
            - {name: RCLONE_CONFIG_BACKUP_TYPE, value: s3}
            - {name: RCLONE_CONFIG_BACKUP_PROVIDER, value: Other}
            - {name: RCLONE_CONFIG_BACKUP_ENDPOINT, value: 'http://garage-backup.garage-backup-system.svc.cluster.local:3900'}
            - {name: RCLONE_CONFIG_BACKUP_REGION, value: garage}
            - {name: RCLONE_CONFIG_BACKUP_FORCE_PATH_STYLE, value: 'true'}
            - name: RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: accessKey}}
            - name: RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: secretKey}}
          volumeMounts: [{name: data, mountPath: /data}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: immich-library-pvc}}]
---
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-html, namespace: nextcloud}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: restore
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c]
          args:
            - |-
              rclone sync backup:nextcloud/pv-backup/html /data -v --links \
                --fast-list --transfers 16 --checkers 32 --stats-one-line &&
              chown -R 33:33 /data &&
              chmod 0770 /data/data
          env:
            - {name: RCLONE_CONFIG_BACKUP_TYPE, value: s3}
            - {name: RCLONE_CONFIG_BACKUP_PROVIDER, value: Other}
            - {name: RCLONE_CONFIG_BACKUP_ENDPOINT, value: 'http://garage-backup.garage-backup-system.svc.cluster.local:3900'}
            - {name: RCLONE_CONFIG_BACKUP_REGION, value: garage}
            - {name: RCLONE_CONFIG_BACKUP_FORCE_PATH_STYLE, value: 'true'}
            - name: RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: accessKey}}
            - name: RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: secretKey}}
          volumeMounts: [{name: data, mountPath: /data}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: nextcloud-nextcloud}}]
EOF
  wait_job immich restore-drill-library
  wait_job nextcloud restore-drill-html

  k apply -f - <<'EOF'
apiVersion: batch/v1
kind: Job
metadata: {name: restore-drill-nextcloud-objects, namespace: nextcloud}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: restore
          image: rclone/rclone:1.74@sha256:623378ad0ff3ebd5cebf77720843c0e02edfe46e2d5b5ac6bed54c6371780dfb
          command: [/bin/sh, -c]
          args: ['rclone sync backup:nextcloud/current live:nextcloud -v --fast-list --transfers 16 --checkers 32 --stats-one-line']
          env:
            - {name: RCLONE_CONFIG_BACKUP_TYPE, value: s3}
            - {name: RCLONE_CONFIG_BACKUP_PROVIDER, value: Other}
            - {name: RCLONE_CONFIG_BACKUP_ENDPOINT, value: 'http://garage-backup.garage-backup-system.svc.cluster.local:3900'}
            - {name: RCLONE_CONFIG_BACKUP_REGION, value: garage}
            - {name: RCLONE_CONFIG_BACKUP_FORCE_PATH_STYLE, value: 'true'}
            - name: RCLONE_CONFIG_BACKUP_ACCESS_KEY_ID
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: accessKey}}
            - name: RCLONE_CONFIG_BACKUP_SECRET_ACCESS_KEY
              valueFrom: {secretKeyRef: {name: garage-secret-backup, key: secretKey}}
            - {name: RCLONE_CONFIG_LIVE_TYPE, value: s3}
            - {name: RCLONE_CONFIG_LIVE_PROVIDER, value: Other}
            - {name: RCLONE_CONFIG_LIVE_ENDPOINT, value: 'http://garage.garage-system.svc.cluster.local:3900'}
            - {name: RCLONE_CONFIG_LIVE_REGION, value: garage}
            - {name: RCLONE_CONFIG_LIVE_FORCE_PATH_STYLE, value: 'true'}
            - name: RCLONE_CONFIG_LIVE_ACCESS_KEY_ID
              valueFrom: {secretKeyRef: {name: garage-secret, key: accessKey}}
            - name: RCLONE_CONFIG_LIVE_SECRET_ACCESS_KEY
              valueFrom: {secretKeyRef: {name: garage-secret, key: secretKey}}
EOF
  wait_job nextcloud restore-drill-nextcloud-objects
}

restore_databases() {
  say "Bootstrapping both databases from the Barman Recovery Points"
  k apply -f - <<'EOF'
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: database, namespace: immich}
spec:
  instances: 1
  imageCatalogRef: {apiGroup: postgresql.cnpg.io, kind: ClusterImageCatalog, name: immich-catalog, major: 16}
  postgresql: {shared_preload_libraries: [vchord.so]}
  storage: {size: 20Gi}
  bootstrap: {recovery: {source: recovery-source}}
  externalClusters:
    - name: recovery-source
      plugin:
        name: barman-cloud.cloudnative-pg.io
        parameters: {barmanObjectName: postgres-backups, serverName: immich-database}
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: {name: database, namespace: nextcloud}
spec:
  instances: 1
  imageName: ghcr.io/cloudnative-pg/postgresql:16
  storage: {size: 20Gi}
  bootstrap: {recovery: {source: recovery-source}}
  externalClusters:
    - name: recovery-source
      plugin:
        name: barman-cloud.cloudnative-pg.io
        parameters: {barmanObjectName: postgres-backups, serverName: nextcloud-database}
EOF
  k wait cluster/database -n immich --for=condition=Ready --timeout=1800s
  k wait cluster/database -n nextcloud --for=condition=Ready --timeout=1800s
  pass "both PostgreSQL recovery clusters reached Ready"
}

wait_cluster_healthy() {
  local namespace="$1" deadline=$((SECONDS + 900)) phase ready
  while (( SECONDS < deadline )); do
    phase=$(k get cluster database -n "$namespace" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    ready=$(k get cluster database -n "$namespace" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    [[ "$phase" == "Cluster in healthy state" && "$ready" == True ]] && return 0
    printf '  database/%s phase=%s ready=%s\n' "$namespace" "${phase:-pending}" "${ready:-pending}"
    sleep 5
  done
  die "database/$namespace did not return to a healthy state"
}

enable_database_archiving() {
  local run_id="$1" namespace server_name
  say "Re-enabling WAL archiving on the recovered databases"
  for namespace in immich nextcloud; do
    server_name="$namespace-database"
    k patch cluster database -n "$namespace" --type=merge -p "{\"spec\":{\"plugins\":[{\"name\":\"barman-cloud.cloudnative-pg.io\",\"isWALArchiver\":true,\"parameters\":{\"barmanObjectName\":\"postgres-backups\",\"serverName\":\"$server_name\"}}]}}"
  done
  # Do not let a still-true Ready condition from the pre-patch generation make
  # the health loop return before CNPG begins the required one-instance restart.
  sleep 5
  wait_cluster_healthy immich
  wait_cluster_healthy nextcloud

  # A restored service that cannot create its next Recovery Point is not fully
  # operational. Prove both archives accept a new base backup after recovery.
  for namespace in immich nextcloud; do
    k apply -f - <<EOF
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata:
  name: post-restore-$run_id
  namespace: $namespace
spec:
  method: plugin
  pluginConfiguration:
    name: barman-cloud.cloudnative-pg.io
  cluster:
    name: database
EOF
    wait_phase backups.postgresql.cnpg.io "$namespace" "post-restore-$run_id" completed
  done
  pass "continuous WAL archiving and post-restore base backups work for both databases"
}

resume_apps() {
  say "Starting restored applications"
  k scale deployment -n immich immich-server immich-machine-learning --replicas=1
  k scale deployment -n nextcloud nextcloud --replicas=1
  k rollout status deployment/immich-server -n immich --timeout=900s
  k rollout status deployment/nextcloud -n nextcloud --timeout=900s
}

write_evidence() {
  local run_id="$1" report="$EVIDENCE_DIR/$run_id.md"
  local data_volume backup_volume
  data_volume=$(terraform -chdir="$TF_DIR" output -raw data_volume_id)
  backup_volume=$(terraform -chdir="$TF_DIR" output -raw backup_volume_id)
  {
    echo "# Restore Drill $run_id"
    echo
    echo "- Target: disposable Hetzner cluster, node \`cc-staging-node-01\`"
    echo "- Operational volume: \`$data_volume\`"
    echo "- Backup volume: \`$backup_volume\` (distinct)"
    echo "- Users: \`drill-alice\`, \`drill-bob\`, \`drill-carol\`"
    echo "- Recovery source: central \`garage-backup\`; no pod archive data used"
    echo "- Database Recovery Points: \`restore-drill-$run_id\` in Immich and Nextcloud"
    echo "- Post-restore database Recovery Points: \`post-restore-$run_id\` in Immich and Nextcloud"
    echo "- Cluster-state Recovery Point: Velero \`restore-drill-$run_id\`"
    echo "- Destructive proof: both databases, Immich library, Nextcloud HTML and Nextcloud live object bucket cleared before restore"
    echo "- Verification: every user logged in through each restored application; 6 Immich originals, 3 albums and 6 Nextcloud files matched their pre-loss SHA-256 digests"
    echo "- Drill findings fixed: recovery bootstrap omits the WAL archiver until recovery completes; Nextcloud ownership/mode is restored before startup"
    echo "- Result: PASS"
  } > "$report"
  pass "evidence written to $report"
}

exercise() {
  ensure_runtime
  assert_disposable_cluster
  local run_id
  run_id=$(tr '[:upper:]' '[:lower:]' < "$RUN_ID_FILE")
  trap stop_forwards RETURN
  wait_source_apps
  if [[ -f "$STATE_JSON" ]] \
     && [[ $(k get backups.velero.io "restore-drill-$run_id" -n velero -o jsonpath='{.status.phase}' 2>/dev/null || true) == Completed ]]; then
    say "Using the seed manifest already captured by the completed Recovery Point"
  else
    say "Seeding three synthetic users through Immich and Nextcloud"
    app_data seed
  fi
  pause_gitops_and_apps
  create_recovery_points "$run_id"
  wipe_primary_data "$run_id"
  restore_secrets "$run_id"
  restore_files
  restore_databases
  enable_database_archiving "$run_id"
  resume_apps
  say "Verifying restored bytes and metadata through both applications"
  app_data verify
  write_evidence "$run_id"
}

destroy() {
  load_hcloud_token
  ensure_runtime
  say "Destroying disposable Hetzner restore-drill resources"
  terraform -chdir="$TF_DIR" init -input=false
  terraform -chdir="$TF_DIR" destroy -auto-approve -input=false
  pass "disposable VM, volumes, firewall and cloud SSH key destroyed"
  if [[ "$RUNTIME_DIR" == /tmp/smallworlds-restore-drill ]]; then
    find "$RUNTIME_DIR" -mindepth 1 -delete
    rmdir "$RUNTIME_DIR"
    pass "synthetic credentials, kubeconfig and ephemeral local SSH key removed"
  fi
}

all() {
  local rc=0
  provision || rc=$?
  if [[ $rc -eq 0 ]]; then exercise || rc=$?; fi
  if [[ "${KEEP_CLUSTER:-0}" == 1 ]]; then
    printf "${YELLOW}KEEP_CLUSTER=1: disposable cluster retained for inspection${NC}\n"
  else
    destroy || rc=$?
  fi
  return "$rc"
}

case "${1:-}" in
  provision) provision;;
  deploy) deploy;;
  exercise) exercise;;
  destroy) destroy;;
  all) all;;
  *) usage; exit 2;;
esac
