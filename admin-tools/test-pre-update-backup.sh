#!/usr/bin/env bash
# Tests the pre-update backup hook (infrastructure/kubernetes/bases/pre-update-backup)
# against a stubbed kubectl: when it backs up, when it does not, and that a failed
# or overdue backup fails the hook so the update is held back.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
failures=0
pass() { echo "  ok    $1"; }
fail() { echo "  FAIL  $1"; failures=$((failures + 1)); }

# The Job's script is the block scalar after "- |" (no PyYAML needed in CI).
python3 - "$ROOT/infrastructure/kubernetes/bases/pre-update-backup/pre-update-backup.yaml" "$WORK/hook.sh" <<'EOF'
import sys
lines = open(sys.argv[1]).read().split("\n")
start = next(i for i, l in enumerate(lines) if l.strip() == "- |" and "kind: Job" in "\n".join(lines[:i]))
indent = len(lines[start + 1]) - len(lines[start + 1].lstrip())
script = []
for l in lines[start + 1:]:
    if l.strip() and len(l) - len(l.lstrip()) < indent:
        break
    script.append(l[indent:])
open(sys.argv[2], "w").write("\n".join(script))
EOF

mkdir -p "$WORK/bin" "$WORK/sa"
echo -n tenant > "$WORK/sa/namespace"
sed -i "s|/var/run/secrets/kubernetes.io/serviceaccount/namespace|$WORK/sa/namespace|" "$WORK/hook.sh"
sed -i 's|sleep 10|sleep 0|' "$WORK/hook.sh"

# The stub answers from files in $STATE and logs every call to $STATE/calls.
cat > "$WORK/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$STATE/calls"
args="$*"
case "$args" in
  *"get clusters.postgresql.cnpg.io"*"jsonpath={.spec.imageName}"*) cat "$STATE/db_image" 2>/dev/null ;;
  *"get clusters.postgresql.cnpg.io"*"isWALArchiver"*) echo -n barman-cloud.cloudnative-pg.io ;;
  *"get clusters.postgresql.cnpg.io"*) [[ -f "$STATE/cluster" ]] || exit 1 ;;
  *"get deployment/app"*) cat "$STATE/live" 2>/dev/null ;;
  *"get configmap pre-update-backup-skip"*) [[ -f "$STATE/skip" ]] || exit 1 ;;
  *"delete configmap"*) rm -f "$STATE/skip" ;;
  *"get backups.postgresql.cnpg.io -l smallworlds.network/pre-update="*) cat "$STATE/done_before" 2>/dev/null ;;
  *"create -f -"*) cat > "$STATE/created" ;;
  *"get backups.postgresql.cnpg.io pre-update-"*"status.phase"*) cat "$STATE/phase" ;;
  *"get backups.postgresql.cnpg.io pre-update-"*"status.error"*) echo -n "barman-cloud: upload refused" ;;
  *"get backups.postgresql.cnpg.io -l smallworlds.network/pre-update --sort-by"*)
      printf 'backup/pre-update-1\nbackup/pre-update-2\nbackup/pre-update-3\nbackup/pre-update-4\n' ;;
  *"delete backup/"*) echo "$args" >> "$STATE/deleted" ;;
esac
EOF
chmod +x "$WORK/bin/kubectl"

run() {  # run <name>, with the state prepared by the caller in $STATE
    CLUSTER=database WORKLOAD=deployment/app CONTAINER=app \
    DESIRED_IMAGE="${DESIRED_IMAGE:-app:2}" DESIRED_DB_IMAGE="${DESIRED_DB_IMAGE:-}" \
    TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-60}" KEEP=3 PATH="$WORK/bin:$PATH" STATE="$STATE" \
        bash "$WORK/hook.sh" > "$STATE/out" 2>&1
    echo $? > "$STATE/exit"
}
fresh() { STATE="$WORK/$1"; mkdir -p "$STATE"; touch "$STATE/calls"; }
exit_of() { cat "$STATE/exit"; }

echo "pre-update backup hook"

fresh first-install
run
[[ $(exit_of) == 0 && ! -f $STATE/created ]] && pass "a first install has nothing to back up" || fail "a first install has nothing to back up"

fresh unchanged; touch "$STATE/cluster"; echo -n app:2 > "$STATE/live"
run
[[ $(exit_of) == 0 && ! -f $STATE/created ]] && grep -q "no backup needed" "$STATE/out" \
    && pass "an unchanged image is not backed up" || fail "an unchanged image is not backed up"

fresh changed; touch "$STATE/cluster"; echo -n app:1 > "$STATE/live"; echo -n completed > "$STATE/phase"
run
if [[ $(exit_of) == 0 ]] && grep -q "method: plugin" "$STATE/created" \
    && grep -q "deployment/app app:1 -> app:2" "$STATE/created"; then
    pass "a new image is backed up through the barman plugin, naming the change"
else
    fail "a new image is backed up through the barman plugin, naming the change"; cat "$STATE/out"
fi
[[ $(wc -l < "$STATE/deleted") == 1 ]] && grep -q "pre-update-1" "$STATE/deleted" \
    && pass "only the newest three pre-update Backups are kept" || fail "only the newest three pre-update Backups are kept"

fresh db-changed; touch "$STATE/cluster"; echo -n app:2 > "$STATE/live"
echo -n postgresql:16 > "$STATE/db_image"; echo -n completed > "$STATE/phase"
DESIRED_DB_IMAGE=postgresql:17 run
[[ $(exit_of) == 0 && -f $STATE/created ]] && grep -q "PostgreSQL postgresql:16 -> postgresql:17" "$STATE/created" \
    && pass "a new PostgreSQL image is backed up too" || fail "a new PostgreSQL image is backed up too"

fresh failed; touch "$STATE/cluster"; echo -n app:1 > "$STATE/live"; echo -n failed > "$STATE/phase"
run
[[ $(exit_of) == 1 ]] && grep -q "upload refused" "$STATE/out" && grep -q "held back" "$STATE/out" \
    && pass "a failed backup fails the hook and says why" || fail "a failed backup fails the hook and says why"

fresh overdue; touch "$STATE/cluster"; echo -n app:1 > "$STATE/live"; echo -n running > "$STATE/phase"
TIMEOUT_SECONDS=0 run
[[ $(exit_of) == 1 ]] && grep -q "did not complete within" "$STATE/out" \
    && pass "an overdue backup fails the hook" || fail "an overdue backup fails the hook"

fresh skip; touch "$STATE/cluster" "$STATE/skip"; echo -n app:1 > "$STATE/live"
run
[[ $(exit_of) == 0 && ! -f $STATE/created && ! -f $STATE/skip ]] \
    && pass "pre-update-backup-skip skips once and is removed" || fail "pre-update-backup-skip skips once and is removed"

fresh retry; touch "$STATE/cluster"; echo -n app:1 > "$STATE/live"; echo pre-update-20261112 > "$STATE/done_before"
run
[[ $(exit_of) == 0 && ! -f $STATE/created ]] && grep -q "already backed up" "$STATE/out" \
    && pass "a retried update is not backed up twice" || fail "a retried update is not backed up twice"

echo
if [[ $failures -eq 0 ]]; then echo "All checks passed."; else echo "$failures check(s) failed."; exit 1; fi
