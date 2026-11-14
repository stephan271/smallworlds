#!/usr/bin/env bash
# Ring 0's storage checks (plans-and-walkthroughs/design-update-strategy.md §3.2,
# "Object storage"), run by the Canary Follower as a Job cut from the CronJob in
# storage-checks-job.yaml, after the e2e and data checks.
#
# Ring 0 stores everything at an external S3 provider, like Ring 1, but keeps
# both Garage instances and the offsite replicator running beside it, unused
# (its overlay is generated with --keep-garage, admin-tools/generate_storage_patches.py),
# so that an update to them is verified like any other. This Job is their only
# user. For each commit:
#
#   1. both Garage instances are up and have a layout (assigned on the first run,
#      as bases/garage-init-job does for a community in Garage mode);
#   2. on each, a key and the bucket ring0-probe, made with the same `garage`
#      commands the init jobs use; then
#        - the object "first", written on the first run, still reads back as
#          written: the data survived every update since. Its content is also
#          kept in the ConfigMap canary-storage-probe, so an instance that lost
#          its data cannot pass by writing it afresh;
#        - the object "latest" is written with this commit and reads back;
#   3. the replicator: one Job cut from its suspended CronJob — its image, its
#      script — copies ring0-probe from garage-backup to the operational
#      instance (bucket ring0-offsite), and this commit's "latest" arrives there.
#
# A cluster without the Application garage passes with a note. The last
# "CHECKS " line goes into the commit status; "CHECKS-INFRA " (the Kubernetes API
# failed us) is no verdict on the commit.
set -uo pipefail

CHECK_SHA="${CHECK_SHA:-main}"
GARAGE_NS="${GARAGE_NS:-garage-system}"
GARAGE_BACKUP_NS="${GARAGE_BACKUP_NS:-garage-backup-system}"
GARAGE_ENDPOINT="${GARAGE_ENDPOINT:-http://garage.garage-system.svc.cluster.local:3900}"
GARAGE_BACKUP_ENDPOINT="${GARAGE_BACKUP_ENDPOINT:-http://garage-backup.garage-backup-system.svc.cluster.local:3900}"
REPLICATOR_NS="${REPLICATOR_NS:-backup-replicator}"
STATE_CONFIGMAP="${STATE_CONFIGMAP:-canary-storage-probe}"
NAMESPACE="${NAMESPACE:-$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace 2>/dev/null || echo canary-follower)}"
READY_TIMEOUT="${READY_TIMEOUT:-300}"
REPLICATOR_TIMEOUT="${REPLICATOR_TIMEOUT:-600}"
WORK="${WORK:-/tmp/storage-checks}"

BUCKET=ring0-probe
OFFSITE=ring0-offsite
KEY=ring0-probe-key
REGION=garage  # Garage's s3_region (apps/garage.yaml); anything else is a 400

mkdir -p "$WORK"
# Garage answers path-style only: http://<host>/<bucket>/<key>.
printf '[default]\ns3 =\n    addressing_style = path\n' > "$WORK/aws-config"
export AWS_CONFIG_FILE="$WORK/aws-config"
infra() { echo "CHECKS-INFRA storage: $*"; exit 1; }
fail() { echo "CHECKS storage: $*"; exit 1; }
step() { echo ">>> $*"; }

# ------------------------------------------------------------------ helpers

# The Garage CLI inside an instance's pod; stderr stays in the Job's log.
garage() { local ns="$1" pod="$2"; shift 2; kubectl exec -n "$ns" "$pod" -- /garage "$@"; }

ready_pod() {
  local ns="$1" waited=0 pod
  while (( waited < READY_TIMEOUT )); do
    pod=$(kubectl get pods -n "$ns" -l app.kubernetes.io/name=garage \
          -o jsonpath='{range .items[*]}{.metadata.name} {.status.containerStatuses[0].ready}{"\n"}{end}' \
          2>/dev/null | awk '$2 == "true" { print $1; exit }')
    [[ -n "$pod" ]] && { echo "$pod"; return 0; }
    sleep 10; waited=$((waited + 10))
  done
  return 1
}

# As bases/garage-init-job: assign the node a role once, then apply.
ensure_layout() {
  local ns="$1" pod="$2" nodes node
  garage "$ns" "$pod" status | grep -q 'NO ROLE ASSIGNED' || return 0
  nodes=$(garage "$ns" "$pod" status | grep -E '^[0-9a-f]{16}' | awk '{print $1}')
  for node in $nodes; do
    garage "$ns" "$pod" layout assign -z dc1 -c 10G "$node" || return 1
  done
  garage "$ns" "$pod" layout apply --version 1 || return 1
  ! garage "$ns" "$pod" status | grep -q 'NO ROLE ASSIGNED'
}

# A fresh key each run, as the init jobs make theirs; prints "<id> <secret>".
make_key() {
  local ns="$1" pod="$2" output id secret
  garage "$ns" "$pod" key delete --yes "$KEY" >/dev/null 2>&1 || true
  output=$(garage "$ns" "$pod" key create "$KEY") || return 1
  id=$(grep "Key ID:" <<<"$output" | awk '{print $3}')
  secret=$(grep "Secret key:" <<<"$output" | awk '{print $3}')
  [[ -n "$id" && -n "$secret" ]] || return 1
  echo "$id $secret"
}

grant_bucket() {
  local ns="$1" pod="$2" bucket="$3"
  garage "$ns" "$pod" bucket info "$bucket" >/dev/null 2>&1 \
    || garage "$ns" "$pod" bucket create "$bucket" || return 1
  garage "$ns" "$pod" bucket allow "$bucket" --read --write --owner --key "$KEY"
}

s3() {
  local endpoint="$1" id="$2" secret="$3"; shift 3
  AWS_ACCESS_KEY_ID="$id" AWS_SECRET_ACCESS_KEY="$secret" AWS_DEFAULT_REGION="$REGION" \
  AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required \
    aws --endpoint-url "$endpoint" --cli-connect-timeout 10 --cli-read-timeout 30 s3api "$@"
}

# Object body, or nothing (and status 1) when it cannot be read.
s3_read() {
  local endpoint="$1" id="$2" secret="$3" bucket="$4" key="$5"
  rm -f "$WORK/read"
  s3 "$endpoint" "$id" "$secret" get-object --bucket "$bucket" --key "$key" "$WORK/read" >/dev/null || return 1
  cat "$WORK/read"
}

s3_write() {
  local endpoint="$1" id="$2" secret="$3" bucket="$4" key="$5" body="$6"
  printf '%s' "$body" > "$WORK/write"
  s3 "$endpoint" "$id" "$secret" put-object --bucket "$bucket" --key "$key" --body "$WORK/write" >/dev/null
}

# "repo/name:tag@sha256:…" -> "name:tag"
short_image() { local image="${1%@*}"; echo "${image##*/}"; }

state_get() {
  kubectl get configmap "$STATE_CONFIGMAP" -n "$NAMESPACE" -o "jsonpath={.data.$1}" 2>/dev/null
}

state_set() {
  kubectl get configmap "$STATE_CONFIGMAP" -n "$NAMESPACE" >/dev/null 2>&1 \
    || kubectl create configmap "$STATE_CONFIGMAP" -n "$NAMESPACE" >/dev/null || return 1
  kubectl patch configmap "$STATE_CONFIGMAP" -n "$NAMESPACE" --type merge \
    -p "{\"data\":{\"$1\":\"$2\"}}" >/dev/null
}

# ------------------------------------------------------------------ checks

if ! applications=$(kubectl get applications.argoproj.io -n argocd -o name 2>&1); then
  infra "cannot list Argo CD Applications: ${applications:0:160}"
fi
if ! grep -qx 'application.argoproj.io/garage' <<<"$applications"; then
  echo "CHECKS storage: Garage is not deployed here, not checked"
  exit 0
fi

LATEST="ring0-probe ${CHECK_SHA:0:12} $(date -u +%Y-%m-%dT%H:%M:%SZ)"
declare -A POD ENDPOINT ID SECRET
ENDPOINT[$GARAGE_NS]="$GARAGE_ENDPOINT"
ENDPOINT[$GARAGE_BACKUP_NS]="$GARAGE_BACKUP_ENDPOINT"
first_run=0

for ns in "$GARAGE_NS" "$GARAGE_BACKUP_NS"; do
  step "$ns: ready, layout, key, bucket"
  POD[$ns]=$(ready_pod "$ns") || fail "$ns: no Garage pod became ready in $((READY_TIMEOUT / 60)) min"
  pod="${POD[$ns]}"
  ensure_layout "$ns" "$pod" || fail "$ns: could not assign a layout"
  credentials=$(make_key "$ns" "$pod") || fail "$ns: could not create a key (garage key create)"
  ID[$ns]="${credentials% *}"; SECRET[$ns]="${credentials#* }"
  grant_bucket "$ns" "$pod" "$BUCKET" || fail "$ns: could not create or grant bucket $BUCKET"

  step "$ns: data written on the first run"
  e="${ENDPOINT[$ns]}" id="${ID[$ns]}" secret="${SECRET[$ns]}"
  expected=$(state_get "first-$ns")
  if [[ -z "$expected" ]]; then
    expected="ring0-probe first ${CHECK_SHA:0:12} $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    s3_write "$e" "$id" "$secret" "$BUCKET" first "$expected" || fail "$ns: could not write the first object"
    state_set "first-$ns" "$expected" || infra "could not record the first object in ConfigMap $STATE_CONFIGMAP"
    first_run=1
  fi
  got=$(s3_read "$e" "$id" "$secret" "$BUCKET" first) \
    || fail "$ns: the object written on the first run is gone (${expected##* })"
  [[ "$got" == "$expected" ]] || fail "$ns: the object written on the first run reads back changed"

  step "$ns: write and read this commit"
  s3_write "$e" "$id" "$secret" "$BUCKET" latest "$LATEST" || fail "$ns: could not write an object"
  got=$(s3_read "$e" "$id" "$secret" "$BUCKET" latest) || fail "$ns: could not read back an object"
  [[ "$got" == "$LATEST" ]] || fail "$ns: an object read back different from what was written"
done

step "replicator: copy $BUCKET from $GARAGE_BACKUP_NS to $GARAGE_NS"
grant_bucket "$GARAGE_NS" "${POD[$GARAGE_NS]}" "$OFFSITE" || fail "$GARAGE_NS: could not create bucket $OFFSITE"
# The replicator syncs source:<bucket> to dest:<bucket>. Here "dest" is an alias
# into the operational instance's ring0-offsite, so the copy cannot overwrite
# the bucket it reads its "latest" from.
cat > "$WORK/rclone.conf" <<EOF
[source]
type = s3
provider = Other
endpoint = $GARAGE_BACKUP_ENDPOINT
region = $REGION
force_path_style = true
access_key_id = ${ID[$GARAGE_BACKUP_NS]}
secret_access_key = ${SECRET[$GARAGE_BACKUP_NS]}

[operational]
type = s3
provider = Other
endpoint = $GARAGE_ENDPOINT
region = $REGION
force_path_style = true
access_key_id = ${ID[$GARAGE_NS]}
secret_access_key = ${SECRET[$GARAGE_NS]}

[dest]
type = alias
remote = operational:$OFFSITE
EOF
kubectl create secret generic replicator-config-secret -n "$REPLICATOR_NS" \
    --from-file=rclone.conf="$WORK/rclone.conf" --dry-run=client -o yaml \
  | kubectl apply -f - >/dev/null || infra "could not write the replicator's rclone config"

# Earlier probe runs: keep none, so a failed one does not keep KubeJobFailed firing.
kubectl delete jobs -n "$REPLICATOR_NS" -l app=ring0-storage-probe --ignore-not-found >/dev/null 2>&1 || true
job="ring0-probe-${CHECK_SHA:0:12}-$(date +%s)"
job="${job:0:63}"
replicator=$(kubectl get cronjob backup-replicator -n "$REPLICATOR_NS" \
        -o jsonpath='{.spec.jobTemplate.spec.template.spec.containers[0].image}' 2>/dev/null) \
  || fail "the replicator's CronJob is missing"
image=$(short_image "$replicator")
if ! kubectl create job "$job" -n "$REPLICATOR_NS" --from=cronjob/backup-replicator \
       --dry-run=client -o json \
     | jq --arg bucket "$BUCKET" '
         .metadata.labels.app = "ring0-storage-probe"
         | .spec.backoffLimit = 1
         | .spec.activeDeadlineSeconds = 900
         | .spec.ttlSecondsAfterFinished = 86400
         | .spec.template.spec.containers[0].env |=
             ((. // []) | map(select(.name != "REPLICATE_BUCKETS"))
              + [{"name": "REPLICATE_BUCKETS", "value": $bucket}])' \
     | kubectl create -f - >/dev/null; then
  infra "could not start a replicator Job"
fi

waited=0 outcome=""
while (( waited < REPLICATOR_TIMEOUT )); do
  outcome=$(kubectl get job "$job" -n "$REPLICATOR_NS" -o jsonpath='{.status.succeeded}/{.status.failed}' 2>/dev/null)
  [[ "$outcome" == 1/* ]] && break
  [[ "$outcome" == */[1-9]* ]] && break
  sleep 10; waited=$((waited + 10))
done
if [[ "$outcome" != 1/* ]]; then
  echo "--- replicator log"
  kubectl logs -n "$REPLICATOR_NS" "job/$job" --tail=40 2>&1 || true
  if (( waited >= REPLICATOR_TIMEOUT )); then
    fail "the replicator ($image) did not finish in $((REPLICATOR_TIMEOUT / 60)) min"
  fi
  fail "the replicator ($image) failed"
fi
kubectl logs -n "$REPLICATOR_NS" "job/$job" --tail=20 2>&1 || true
got=$(s3_read "${ENDPOINT[$GARAGE_NS]}" "${ID[$GARAGE_NS]}" "${SECRET[$GARAGE_NS]}" "$OFFSITE" "$BUCKET/latest") \
  || fail "the replicator ($image) finished, but this commit's object did not arrive"
[[ "$got" == "$LATEST" ]] || fail "the replicator ($image) copied an old object"

version=$(short_image "$(kubectl get pod "${POD[$GARAGE_NS]}" -n "$GARAGE_NS" \
          -o jsonpath='{.spec.containers[0].image}' 2>/dev/null)")
version="${version#*:}"
if (( first_run )); then
  echo "CHECKS storage: Garage ${version:-?}, rclone ${image#*:} ok (first data)"
else
  echo "CHECKS storage: Garage ${version:-?}, rclone ${image#*:} ok"
fi
