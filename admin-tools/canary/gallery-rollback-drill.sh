#!/usr/bin/env bash
# Roll Gallery back to an earlier release on Ring 0, the way Gallery documents it
# ("Rolling back to an earlier release", docs/install/upgrading.md), translated
# from docker compose to this cluster. A drill: design-update-strategy.md §8 step 4.
#
#   admin-tools/canary/gallery-rollback-drill.sh <running tag> <target tag>
#   e.g. admin-tools/canary/gallery-rollback-drill.sh v5.10.0 v5.8.0
#
# Going back is not a tag change. The earlier release refuses to start on a
# database holding Gallery migrations it does not ship, so the RUNNING image
# reverts them one by one first; a release before the gallery_migrations ledger
# (all of 5.7, 5.8) also needs the records copied back into kysely_migrations.
#
# What it does, asking before each change:
#   1. stops Argo CD from undoing the drill (automation off on smallworlds-root
#      and on immich), and stops Gallery;
#   2. reverts, with the running image, every Gallery migration the target does
#      not ship (one Job per migration, each one's log checked);
#   3. copies the ledger back if the target predates it;
#   4. starts the target release.
# Afterwards: seed.py check; then `--forward` re-enables Argo CD, which brings
# main's release back, and Gallery runs the reverted migrations again.
#
# Needs: kubectl (canary kubeconfig) and docker (to list a release's migrations).
# Before running it: an on-demand CNPG backup of immich/database.
set -euo pipefail

export KUBECONFIG="${KUBECONFIG:-$HOME/.smallworlds/kubeconfigs/local-canary.yaml}"
NS=immich
DB=app
SPLIT=1790900000000-SplitSharedPersonGroups

die() { echo "!!! $*" >&2; exit 1; }
ask() { read -r -p ">>> $1 [y/N] " answer; [[ "$answer" == [yY] ]] || die "stopped before: $1"; }
sql() { kubectl exec -n "$NS" "$PRIMARY" -c postgres -- psql -U postgres -d "$DB" -At -v ON_ERROR_STOP=1 -c "$1"; }

[[ "$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')" ]] || die "no cluster"
kubectl get application -n argocd canary-follower >/dev/null 2>&1 \
    || die "this cluster has no canary-follower: not Ring 0, refusing"

if [[ "${1:-}" == "--forward" ]]; then
    ask "re-enable Argo CD automation (Gallery returns to what main says)"
    kubectl patch application -n argocd smallworlds-root --type merge \
        -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}'
    kubectl patch application -n argocd immich --type merge \
        -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}'
    kubectl annotate application -n argocd immich argocd.argoproj.io/refresh=hard --overwrite
    echo "Watch: curl -s https://photos.canary.smallworlds.network/api/server/version"
    exit 0
fi

RUNNING="${1:?running tag, e.g. v5.10.0}"
TARGET="${2:?target tag, e.g. v5.8.0}"
PRIMARY="$(kubectl get cluster.postgresql.cnpg.io -n "$NS" database -o jsonpath='{.status.currentPrimary}')"
current_image="$(kubectl get deploy -n "$NS" immich-server -o jsonpath='{.spec.template.spec.containers[0].image}')"
[[ "$current_image" == *":$RUNNING" ]] || die "immich-server runs $current_image, not $RUNNING"

work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
# Anonymous pulls: Gallery's images are public, and stale ghcr.io credentials in
# the default Docker config make every pull fail with "denied".
mkdir -p "$work/docker"; echo '{}' > "$work/docker/config.json"
docker() { DOCKER_CONFIG="$work/docker" command docker "$@"; }
echo "Listing the Gallery migrations $TARGET ships…"
docker run --rm --entrypoint ls "ghcr.io/open-noodle/gallery-server:$TARGET" \
    /usr/src/app/server/dist/schema/migrations-gallery | sed -n 's/\.js$//p' | sort > "$work/target"
[[ -s "$work/target" ]] || die "could not list $TARGET's migrations"
# The separate ledger and its revert tool arrived together (v5.9).
ledger_in_target="$(docker run --rm --entrypoint sh "ghcr.io/open-noodle/gallery-server:$TARGET" -c \
    'test -f /usr/src/app/server/dist/bin/gallery-migrations.js && echo yes || echo no')"

# Gallery's revert tool undoes Gallery migrations only. A Gallery release on a newer
# Immich base also ran Immich migrations, which nothing undoes; the earlier
# release then refuses to start ("previously executed migration … is missing").
# Found on Ring 0, 2026-11-11: v5.10.0 (Immich 3.3.5) -> v5.8.0 (Immich 3.3.1)
# stopped on 1792213725597-AddAssetFaceAssetState. The only way back across a
# base change is the backup taken before the update.
docker run --rm --entrypoint ls "ghcr.io/open-noodle/gallery-server:$TARGET" \
    /usr/src/app/server/dist/schema/migrations | sed -n 's/\.js$//p' | sort > "$work/target-base"
base_extra="$(sql 'SELECT name FROM kysely_migrations' | sort | comm -23 - <(sort -u "$work/target-base" "$work/target"))"
[[ -z "$base_extra" ]] || die "the database has Immich migrations $TARGET does not ship: $(echo $base_extra)
    Reverting cannot reach $TARGET; restore the backup taken before the update instead."

extra() { sql 'SELECT name FROM gallery_migrations ORDER BY name' | sort | comm -23 - "$work/target"; }
echo "Applied but not in $TARGET:"; extra | sed 's/^/  /'
echo "Backups:"; kubectl get backup -n "$NS" | tail -3

# 1 — nothing may put Gallery back while the database is half reverted.
ask "turn off Argo CD automation for smallworlds-root and immich, stop Gallery"
kubectl patch application -n argocd smallworlds-root --type json -p '[{"op":"remove","path":"/spec/syncPolicy/automated"}]'
kubectl patch application -n argocd immich --type json -p '[{"op":"remove","path":"/spec/syncPolicy/automated"}]'
kubectl scale deploy -n "$NS" immich-server immich-machine-learning --replicas=0
for _ in $(seq 1 60); do
    pods="$(kubectl get deploy -n "$NS" immich-server immich-machine-learning -o jsonpath='{.items[*].status.replicas}')"
    [[ -z "${pods// /}" || "${pods// /}" =~ ^0*$ ]] && break
    sleep 3
done

# 2 — one revert per Job, so each one's log says what it undid.
job() {
    local name="gallery-revert-$(date +%s)"
    kubectl get deploy -n "$NS" immich-server -o json | python3 -c '
import json, sys
d = json.load(sys.stdin)["spec"]["template"]["spec"]
c = d["containers"][0]
job = {"apiVersion": "batch/v1", "kind": "Job",
       "metadata": {"name": sys.argv[1], "namespace": sys.argv[2]},
       "spec": {"backoffLimit": 0, "ttlSecondsAfterFinished": 86400, "template": {"spec": {
           "restartPolicy": "Never", "serviceAccountName": d.get("serviceAccountName"),
           "containers": [{"name": "revert", "image": sys.argv[3], "env": c.get("env", []),
                           "workingDir": "/usr/src/app",
                           "command": ["node", "server/dist/bin/gallery-migrations.js", "revert"]}]}}}}
print(json.dumps(job))' "$name" "$NS" "ghcr.io/open-noodle/gallery-server:$RUNNING" | kubectl apply -f - >/dev/null
    kubectl wait -n "$NS" --for=condition=complete "job/$name" --timeout=300s >/dev/null \
        || { kubectl logs -n "$NS" "job/$name" | tail -20; die "revert job $name failed"; }
    kubectl logs -n "$NS" "job/$name" | grep -o 'Reverted migration "[^"]*"' | sed 's/.*"\(.*\)"/\1/'
}

ask "revert $(extra | wc -l) Gallery migrations with $RUNNING"
for _ in $(seq 1 40); do
    mapfile -t left < <(extra)
    (( ${#left[@]} )) || break
    if [[ "${left[*]}" == "$SPLIT" ]] && (( $(sql 'SELECT count(*) FROM person_group_split') > 0 )); then
        echo "  $SPLIT: people are split; deleting only its record, as Gallery documents"
        sql "DELETE FROM gallery_migrations WHERE name = '$SPLIT'"
        continue
    fi
    reverted="$(job)"
    [[ -n "$reverted" ]] || die "a revert job logged no reverted migration"
    grep -qxF "$reverted" "$work/target" \
        && die "reverted $reverted, which $TARGET ships — stop; that is a base migration, restore the backup"
    echo "  reverted $reverted"
done
[[ -z "$(extra)" ]] || die "still applied: $(extra | paste -sd' ')"

# 3 — releases before the separate ledger read kysely_migrations.
if [[ "$ledger_in_target" == no ]]; then
    ask "$TARGET predates gallery_migrations: copy the records back into kysely_migrations"
    sql 'INSERT INTO kysely_migrations ("name", "timestamp") SELECT "name", "timestamp" FROM gallery_migrations ON CONFLICT ("name") DO NOTHING'
fi

# 4
ask "start Gallery $TARGET"
kubectl set image -n "$NS" deploy/immich-server "main=ghcr.io/open-noodle/gallery-server:$TARGET"
kubectl set image -n "$NS" deploy/immich-machine-learning "main=ghcr.io/open-noodle/gallery-ml:$TARGET"
kubectl scale deploy -n "$NS" immich-server immich-machine-learning --replicas=1
kubectl rollout status -n "$NS" deploy/immich-server --timeout=600s
curl -s "https://photos.canary.smallworlds.network/api/server/version"; echo
echo "Now: admin-tools/canary/seed.py check — then: $0 --forward"
