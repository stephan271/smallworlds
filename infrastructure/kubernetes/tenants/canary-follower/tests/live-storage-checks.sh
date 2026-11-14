#!/usr/bin/env bash
# checks/storage-checks.sh against two real Garage instances and the real
# replicator script, in Docker — no cluster needed.
#
# A stub kubectl stands in for the API: `kubectl exec … -- /garage` becomes the
# Garage CLI over RPC to the right container, and "creating" the replicator Job
# runs that Job's own script (from the rendered tenants/backup-replicator) with
# the rclone binary of its pinned image. Images come from the manifests: the
# probe's from storage-checks-job.yaml, Garage's from the chart version in
# apps/garage.yaml (or GARAGE_IMAGE=dxflrs/garage:vX to try another release).
#
#   infrastructure/kubernetes/tenants/canary-follower/tests/live-storage-checks.sh
#
# Runs: a first run, a second run that must find the first run's data, and two
# that must fail — the first object lost, and a replicator that cannot write.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TENANT="$(dirname "$HERE")"
K8S="$(cd "$TENANT/../.." && pwd)"
WORK="$(mktemp -d)"
NET="sw-storage-checks-$$"
RPC_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"

cleanup() {
  docker rm -f "$NET-op" "$NET-bk" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

yaml_get() { python3 -c 'import sys,yaml; d=[x for x in yaml.safe_load_all(open(sys.argv[1])) if x]; print(eval(sys.argv[2], {"d": d}))' "$@"; }
PROBE_IMAGE="$(yaml_get "$TENANT/storage-checks-job.yaml" \
  'next(x for x in d if x["kind"]=="CronJob")["spec"]["jobTemplate"]["spec"]["template"]["spec"]["containers"][0]["image"]')"
if [[ -z "${GARAGE_IMAGE:-}" ]]; then
  chart="$(yaml_get "$K8S/apps/garage.yaml" 'd[0]["spec"]["source"]["targetRevision"]')"
  app_version="$(helm show chart garage --repo https://datahub-local.github.io/garage-helm --version "$chart" \
                 | awk '/^appVersion:/ {print $2}' | tr -d '"')"
  GARAGE_IMAGE="dxflrs/garage:$app_version"
fi
kubectl kustomize "$K8S/tenants/backup-replicator" \
  | python3 -c 'import sys,yaml,json; print(json.dumps(next(d for d in yaml.safe_load_all(sys.stdin) if d and d["kind"]=="CronJob")))' \
  > "$WORK/cronjob.json"
RCLONE_IMAGE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["spec"]["jobTemplate"]["spec"]["template"]["spec"]["containers"][0]["image"])' "$WORK/cronjob.json")"
echo "probe $PROBE_IMAGE"; echo "garage $GARAGE_IMAGE"; echo "replicator $RCLONE_IMAGE"

# Binaries the probe container lacks, taken from their own images.
mkdir -p "$WORK/bin" "$WORK/stub" "$WORK/state"
for pair in "$GARAGE_IMAGE:/garage:garage" "$RCLONE_IMAGE:/usr/local/bin/rclone:rclone"; do
  image="${pair%%:/*}"; rest="${pair#*:/}"; path="/${rest%:*}"; name="${rest##*:}"
  id="$(docker create "$image")"
  docker cp "$id:$path" "$WORK/bin/$name" >/dev/null
  docker rm "$id" >/dev/null
done

docker network create "$NET" >/dev/null
for side in op bk; do
  cat > "$WORK/garage-$side.toml" <<EOF
metadata_dir = "/var/lib/garage/meta"
data_dir = "/var/lib/garage/data"
db_engine = "lmdb"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_secret = "$RPC_SECRET"
[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
EOF
  docker run -d --name "$NET-$side" --network "$NET" \
    -v "$WORK/garage-$side.toml:/etc/garage.toml:ro" "$GARAGE_IMAGE" >/dev/null
done
for side in op bk; do
  for _ in $(seq 60); do docker exec "$NET-$side" /garage status >/dev/null 2>&1 && break; sleep 1; done
  node="$(docker exec "$NET-$side" /garage node id -q 2>/dev/null | cut -d@ -f1)"
  echo "$node@$NET-$side:3901" > "$WORK/state/node-$side"
done

cat > "$WORK/stub/kubectl" <<'STUB'
#!/bin/bash
# Just enough of kubectl for storage-checks.sh; state under /state.
args=("$@")
ns=""; for ((i = 0; i < ${#args[@]}; i++)); do [[ "${args[i]}" == -n ]] && ns="${args[i+1]}"; done
side() { [[ "$1" == garage-backup-system ]] && echo bk || echo op; }
case "$1 $2" in
  "get applications.argoproj.io") echo "application.argoproj.io/garage" ;;
  "get pods") echo "garage-0 true" ;;
  "get pod") echo "$GARAGE_IMAGE" ;;
  "exec -n")
    shift 6  # exec -n <ns> <pod> -- /garage
    exec /stub-bin/garage -h "$(cat /state/node-$(side "$ns"))" -s "$RPC_SECRET" "$@" ;;
  "get configmap")
    [[ -d /state/cm ]] || exit 1
    for a in "$@"; do [[ "$a" == jsonpath=* ]] && key="${a#jsonpath=\{.data.}" && key="${key%\}}"; done
    [[ -n "${key:-}" ]] && cat "/state/cm/$key" 2>/dev/null; exit 0 ;;
  "create configmap") mkdir -p /state/cm ;;
  "patch configmap")
    for ((i = 0; i < ${#args[@]}; i++)); do [[ "${args[i]}" == -p ]] && patch="${args[i+1]}"; done
    echo "$patch" | jq -r '.data | to_entries[] | "\(.key)\t\(.value)"' \
      | while IFS=$'\t' read -r k v; do printf '%s' "$v" > "/state/cm/$k"; done ;;
  "create secret")
    for a in "$@"; do [[ "$a" == --from-file=rclone.conf=* ]] && cp "${a#--from-file=rclone.conf=}" /state/rclone.conf; done ;;
  "apply -f") cat >/dev/null ;;
  "delete jobs") rm -f /state/job-* ;;
  "get cronjob") jq -r '.spec.jobTemplate.spec.template.spec.containers[0].image' /state/cronjob.json ;;
  "create job")
    jq --arg name "$3" '{apiVersion: "batch/v1", kind: "Job", metadata: {name: $name},
                          spec: .spec.jobTemplate.spec}' /state/cronjob.json ;;
  "create -f")
    # Run the Job's own container script, with its own env, to completion.
    job="$(cat)"; container="$(jq '.spec.template.spec.containers[0]' <<<"$job")"
    script="$(jq -r '.args[0]' <<<"$container")"
    env_args=(); while IFS=$'\t' read -r k v; do env_args+=("$k=$v"); done \
      < <(jq -r '.env[] | "\(.name)\t\(.value)"' <<<"$container")
    if env "${env_args[@]}" PATH="/stub-bin:$PATH" RCLONE_CONFIG=/state/rclone.conf \
         sh -c "$script" > /state/job-log 2>&1; then
      echo "1/" > /state/job-status
    else
      echo "/1" > /state/job-status
    fi ;;
  "get job") cat /state/job-status ;;
  "logs -n") cat /state/job-log ;;
  *) echo "stub kubectl: unhandled: $*" >&2; exit 1 ;;
esac
exit 0
STUB
chmod +x "$WORK/stub/kubectl"

probe() {  # <sha> [cronjob.json]
  local sha="$1" cronjob="${2:-$WORK/cronjob.json}"
  docker run --rm --network "$NET" --user "$(id -u):$(id -g)" -e HOME=/tmp --read-only --tmpfs /tmp \
    -e CHECK_SHA="$sha" -e GARAGE_IMAGE="$GARAGE_IMAGE" -e RPC_SECRET="$RPC_SECRET" \
    -e NAMESPACE=canary-follower -e READY_TIMEOUT=30 -e REPLICATOR_TIMEOUT=60 \
    -e GARAGE_ENDPOINT="http://$NET-op:3900" -e GARAGE_BACKUP_ENDPOINT="http://$NET-bk:3900" \
    -v "$WORK/stub:/stub:ro" -v "$WORK/bin:/stub-bin:ro" -v "$WORK/state:/state" \
    -v "$cronjob:/state/cronjob.json:ro" \
    -v "$TENANT/checks/storage-checks.sh:/storage-checks.sh:ro" \
    --entrypoint bash "$PROBE_IMAGE" -c 'PATH=/stub:$PATH bash /storage-checks.sh' 2>&1
}
summary() { grep -E '^CHECKS(-INFRA)? ' <<<"$1" | tail -1; }
failures=0
expect() {  # <name> <regex> <output>
  local line; line="$(summary "$3")"
  if [[ "$line" =~ $2 ]]; then echo "ok   $1: $line"
  else echo "FAIL $1: $line"; echo "$3" | tail -30; failures=$((failures + 1)); fi
}

out="$(probe aaaaaaaaaaaa1111)" || true
expect "first run" '^CHECKS storage: Garage v[0-9.]+, rclone [0-9.]+ ok \(first data\)$' "$out"
out="$(probe bbbbbbbbbbbb2222)" || true
expect "second run keeps the data" '^CHECKS storage: Garage v[0-9.]+, rclone [0-9.]+ ok$' "$out"

# A lost first object must fail, not be written again.
id_secret="$(docker exec "$NET-bk" /garage key create lose-it 2>/dev/null | awk '/Key ID:|Secret key:/ {print $3}' | xargs)"
docker exec "$NET-bk" /garage bucket allow ring0-probe --read --write --key lose-it >/dev/null 2>&1
docker run --rm --network "$NET" --entrypoint sh -e AWS_ACCESS_KEY_ID="${id_secret% *}" \
  -e AWS_SECRET_ACCESS_KEY="${id_secret#* }" -e AWS_DEFAULT_REGION=garage "$PROBE_IMAGE" -c \
  "printf '[default]\ns3 =\n    addressing_style = path\n' > /tmp/c; AWS_CONFIG_FILE=/tmp/c aws --endpoint-url http://$NET-bk:3900 s3api delete-object --bucket ring0-probe --key first" >/dev/null
out="$(probe cccccccccccc3333)" || true
expect "lost data fails" '^CHECKS storage: garage-backup-system: the object written on the first run is gone' "$out"
cp "$WORK/state/cm/first-garage-backup-system" "$WORK/first-bk"
rm "$WORK/state/cm/first-garage-backup-system"   # let the next run start that instance afresh

# A replicator that cannot run fails the commit.
jq '.spec.jobTemplate.spec.template.spec.containers[0].args[0] = "echo broken >&2; exit 3"' \
  "$WORK/cronjob.json" > "$WORK/cronjob-broken.json"
out="$(probe dddddddddddd4444 "$WORK/cronjob-broken.json")" || true
expect "broken replicator fails" '^CHECKS storage: the replicator \(rclone:.*\) failed$' "$out"

echo
if (( failures )); then echo "$failures of 4 failed"; exit 1; fi
echo "all 4 passed"
