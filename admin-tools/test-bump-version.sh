#!/usr/bin/env bash
# bump-version.sh cuts a release on a commit beside main in which the base's own
# Applications name the tag, from a commit Ring 0 verified (docs/adr/0060). Runs
# against a throwaway repository with a local bare "origin" and a GitHub API
# made of files, so nothing leaves this machine.
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

git init --quiet --bare "$scratch/origin.git"
git init --quiet -b main "$scratch/work"
cd "$scratch/work"
git config user.name test && git config user.email test@example.invalid
mkdir -p infrastructure/kubernetes/apps admin-tools/lib
cp "$repository_root/admin-tools/bump-version.sh" admin-tools/
cp "$repository_root/admin-tools/lib/ring0_gate.py" admin-tools/lib/
cat > infrastructure/kubernetes/apps/join.yaml <<'APP'
spec:
  source:
    repoURL: 'https://github.com/stephan271/smallworlds.git'
    targetRevision: HEAD
    path: infrastructure/kubernetes/tenants/join
APP
cat > infrastructure/kubernetes/apps/trivy-operator.yaml <<'APP'
spec:
  source:
    repoURL: 'https://aquasecurity.github.io/helm-charts/'
    targetRevision: "0.33.2"
    chart: trivy-operator
APP
cat > infrastructure/kubernetes/apps/elsewhere.yaml <<'APP'
spec:
  source:
    repoURL: https://github.com/someone/else.git
    targetRevision: HEAD
APP
git add -A && git commit --quiet -m base
git tag -a v1.2.43 -m v1.2.43
soaked="$(git rev-parse HEAD)"
echo one > one && git add one && git commit --quiet -m "fresh, green 10 h"
fresh="$(git rev-parse HEAD)"
echo two > two && git add two && git commit --quiet -m "red after green"
red="$(git rev-parse HEAD)"
git remote add origin "$scratch/origin.git"
git push --quiet origin main v1.2.43
main_before="$(git rev-parse main)"

# The fake GitHub: statuses newest first, as the API lists them. "Now" is fixed.
api="$scratch/api"
status_file() { mkdir -p "$api/repos/stephan271/smallworlds/commits/$1"; cat > "$api/repos/stephan271/smallworlds/commits/$1/statuses"; }
status_file "$soaked" <<'JSON'
[{"context": "ring0/verified", "state": "success", "created_at": "2026-11-06T09:00:00Z", "description": "ok"},
 {"context": "ring0/verified", "state": "pending", "created_at": "2026-11-06T08:00:00Z"}]
JSON
status_file "$fresh" <<'JSON'
[{"context": "other/ci", "state": "success", "created_at": "2026-11-10T08:00:00Z"},
 {"context": "ring0/verified", "state": "success", "created_at": "2026-11-10T02:00:00Z", "description": "ok"}]
JSON
status_file "$red" <<'JSON'
[{"context": "ring0/verified", "state": "failure", "created_at": "2026-11-10T11:00:00Z", "description": "Smoke failed: photos"},
 {"context": "ring0/verified", "state": "success", "created_at": "2026-11-10T10:00:00Z"}]
JSON
export SMALLWORLDS_GITHUB_API="file://$api" SMALLWORLDS_GATE_NOW="2026-11-10T12:00:00Z"

failures=0
check() {
    if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; failures=$((failures + 1)); fi
}
tag_count() { git tag | wc -l; }

echo "bump-version — the kind is required and decides the number"
check "no kind is refused" "! bash admin-tools/bump-version.sh >/dev/null 2>&1"
check "an unknown kind is refused" "! bash admin-tools/bump-version.sh feature >/dev/null 2>&1"
check "nothing was tagged by a refusal" "[ \"\$(tag_count)\" = 1 ]"

echo "bump-version — the Ring 0 gate"
bash admin-tools/bump-version.sh patch >/dev/null 2>"$scratch/err"
check "the tag reached origin" \
    "git --git-dir='$scratch/origin.git' rev-parse -q --verify refs/tags/v1.2.44 >/dev/null"
check "a red head and a commit green only 10 h are passed over for one green 99 h" \
    "[ \"\$(git rev-parse 'v1.2.44^{commit}^')\" = '$soaked' ]"
check "the tag records the Ring 0 verdict" \
    "git tag -l --format='%(contents)' v1.2.44 | grep -q 'Ring 0: verified 2026-11-06T09:00:00Z, 99 h'"
check "and the kind" "git tag -l --format='%(contents)' v1.2.44 | grep -q '(patch)'"

SMALLWORLDS_RELEASE_PUSH=0 bash admin-tools/bump-version.sh minor --security >/dev/null 2>&1
check "--security waives the soak but not the verdict: the fresh green commit" \
    "[ \"\$(git rev-parse 'v1.3.0^{commit}^')\" = '$fresh' ]"
check "minor raises the minor number and resets patch" "git rev-parse -q --verify refs/tags/v1.3.0 >/dev/null"
check "the security flag is in the tag" "git tag -l --format='%(contents)' v1.3.0 | grep -q '(minor, security)'"

rm -rf "$api"
check "with no verified commit at all, nothing is released" \
    "! SMALLWORLDS_RELEASE_PUSH=0 bash admin-tools/bump-version.sh major >/dev/null 2>&1"
check "…and no tag appears" "! git rev-parse -q --verify refs/tags/v2.0.0 >/dev/null"
check "the override needs a reason" \
    "! SMALLWORLDS_RELEASE_GATE=off SMALLWORLDS_RELEASE_PUSH=0 bash admin-tools/bump-version.sh major >/dev/null 2>&1"
SMALLWORLDS_RELEASE_GATE=off SMALLWORLDS_RELEASE_GATE_REASON="the pins file was missing" \
    SMALLWORLDS_RELEASE_PUSH=0 bash admin-tools/bump-version.sh major >/dev/null 2>&1
check "with a reason it cuts major from main's head" \
    "[ \"\$(git rev-parse 'v2.0.0^{commit}^')\" = '$main_before' ]"
check "and the tag says it skipped the gate, and why" \
    "git tag -l --format='%(contents)' v2.0.0 | grep -q 'WITHOUT the Ring 0 gate: the pins file was missing'"

echo "bump-version — a release pins the base's Applications to itself"
check "the base's Application names the tag in the release" \
    "git show v1.2.44:infrastructure/kubernetes/apps/join.yaml | grep -q 'targetRevision: v1.2.44'"
check "a Helm chart's version is left alone" \
    "git show v1.2.44:infrastructure/kubernetes/apps/trivy-operator.yaml | grep -q 'targetRevision: \"0.33.2\"'"
check "another repository's HEAD is left alone" \
    "git show v1.2.44:infrastructure/kubernetes/apps/elsewhere.yaml | grep -q 'targetRevision: HEAD'"
check "main itself still tracks HEAD" \
    "git show main:infrastructure/kubernetes/apps/join.yaml | grep -q 'targetRevision: HEAD'"
check "main did not move" "[ \"\$(git rev-parse main)\" = '$main_before' ]"
check "no worktree is left behind" "[ \"\$(git worktree list | wc -l)\" = 1 ]"

if [ "$failures" -ne 0 ]; then
    echo "$failures check(s) failed."
    exit 1
fi
echo "All checks passed."
