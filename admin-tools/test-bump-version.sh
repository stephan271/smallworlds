#!/usr/bin/env bash
# bump-version.sh cuts a release on a commit beside main in which the base's own
# Applications name the tag. Runs against a throwaway repository with a local
# bare "origin", so nothing leaves this machine.
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

git init --quiet --bare "$scratch/origin.git"
git init --quiet -b main "$scratch/work"
cd "$scratch/work"
git config user.name test && git config user.email test@example.invalid
mkdir -p infrastructure/kubernetes/apps admin-tools
cp "$repository_root/admin-tools/bump-version.sh" admin-tools/
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
git remote add origin "$scratch/origin.git"
git push --quiet origin main v1.2.43
main_before="$(git rev-parse main)"

bash admin-tools/bump-version.sh >/dev/null

failures=0
check() {
    if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; failures=$((failures + 1)); fi
}
echo "bump-version — a release pins the base's Applications to itself"
check "the tag reached origin" \
    "git --git-dir='$scratch/origin.git' rev-parse -q --verify refs/tags/v1.2.44 >/dev/null"
check "the base's Application names the tag in the release" \
    "git show v1.2.44:infrastructure/kubernetes/apps/join.yaml | grep -q 'targetRevision: v1.2.44'"
check "a Helm chart's version is left alone" \
    "git show v1.2.44:infrastructure/kubernetes/apps/trivy-operator.yaml | grep -q 'targetRevision: \"0.33.2\"'"
check "another repository's HEAD is left alone" \
    "git show v1.2.44:infrastructure/kubernetes/apps/elsewhere.yaml | grep -q 'targetRevision: HEAD'"
check "main itself still tracks HEAD" \
    "git show main:infrastructure/kubernetes/apps/join.yaml | grep -q 'targetRevision: HEAD'"
check "main did not move" "[ \"\$(git rev-parse main)\" = '$main_before' ]"
check "the release sits directly on main" \
    "[ \"\$(git rev-parse 'v1.2.44^{commit}^')\" = '$main_before' ]"
check "no worktree is left behind" "[ \"\$(git worktree list | wc -l)\" = 1 ]"

if [ "$failures" -ne 0 ]; then
    echo "$failures check(s) failed."
    exit 1
fi
echo "All checks passed."
