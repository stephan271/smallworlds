#!/usr/bin/env bash
# Ring 0's checks for one commit of main (design §3.5 steps 3–4), run by the Canary
# Follower as a Job cut from the CronJob in checks-job.yaml.
#
#   1. fetch that commit's e2e-tests/ and admin-tools/canary/, so the checks always
#      match the code they verify;
#   2. e2e with full OIDC: test users onboarded with a passkey through the real
#      action-token link, then every app's login round trip (tests 09 and 10 skip
#      themselves: they need kubectl, which this Job deliberately does not have);
#   3. the data checks (admin-tools/canary/seed.py check): the example members'
#      photos, files, repository and mail, read back after the update, with a
#      fresh passkey sign-in.
#
# The last "CHECKS <summary>" line is what the follower puts into the commit status.
set -uo pipefail

: "${DOMAIN:?}" "${REPO:?}"
CHECK_SHA="${CHECK_SHA:-main}"  # set by the follower; main for a run by hand
export HOME=/work/home npm_config_cache=/work/npm-cache
mkdir -p "$HOME" /work/src /work/state
say() { echo "CHECKS $*"; }
# Nothing was checked: no verdict on the commit, the follower reports an error.
infra() { echo "CHECKS-INFRA $*"; exit 1; }

if [[ -n "${CHECK_LOCAL_SRC:-}" ]]; then
  # A checkout mounted in, to try uncommitted checks against Ring 0 with docker.
  cp -r "$CHECK_LOCAL_SRC/e2e-tests" "$CHECK_LOCAL_SRC/admin-tools" /work/src/
  rm -rf /work/src/e2e-tests/node_modules
elif ! { echo ">>> fetching ${REPO}@${CHECK_SHA:0:12}"; curl -fsSL --retry 5 --retry-all-errors "https://codeload.github.com/${REPO}/tar.gz/${CHECK_SHA}" \
    | tar -xz --strip-components=1 -C /work/src --wildcards '*/e2e-tests/*' '*/admin-tools/canary/*'; }; then
  infra "could not fetch ${CHECK_SHA:0:12}"
fi

cd /work/src/e2e-tests
echo ">>> npm ci"
npm ci --no-audit --no-fund --loglevel=error || infra "npm ci failed"
# A no-op while the image matches package-lock.json; after Renovate moves one
# without the other it downloads the matching browser instead of failing.
# /ms-playwright belongs to root, so a different browser goes under /work.
if ! npx playwright install chromium >/dev/null 2>&1; then
  export PLAYWRIGHT_BROWSERS_PATH=/work/ms-playwright
  npx playwright install chromium >/dev/null || infra "no Chromium for this Playwright"
fi

echo ">>> e2e: provisioning test users"
e2e="e2e not run"
e2e_ok=0
if npx tsx setup/provision-test-users.ts; then
  echo ">>> e2e: full OIDC"
  FULL_OIDC=1 PLAYWRIGHT_JSON_OUTPUT_NAME=/work/e2e.json npx playwright test --reporter=list,json
  e2e_exit=$?
  e2e="$(python3 - <<'EOF'
import json
try:
    stats = json.load(open("/work/e2e.json"))["stats"]
except Exception as error:
    print(f"e2e report unreadable ({type(error).__name__})")
else:
    parts = [f"{stats.get('expected', 0)} passed"]
    for key, word in (("unexpected", "failed"), ("flaky", "flaky"), ("skipped", "skipped")):
        if stats.get(key):
            parts.append(f"{stats[key]} {word}")
    print("e2e " + ", ".join(parts))
EOF
)"
  [[ $e2e_exit -eq 0 ]] && e2e_ok=1
else
  e2e="e2e: test users not provisioned"
fi

echo ">>> data checks"
cp /secrets/seed-state.json /work/state/seed-state.json
data_out="$(python3 /work/src/admin-tools/canary/seed.py check --domain "$DOMAIN" \
            --state /work/state/seed-state.json 2>&1)"
data_exit=$?
echo "$data_out"
apps_ok=$(grep -c '^  [a-z]*: ok$' <<<"$data_out")
apps_all=$(grep -cE '^  [a-z]*: (ok|[0-9]+ failed)$' <<<"$data_out")
if [[ $data_exit -eq 0 ]]; then
  data="data ${apps_ok}/${apps_all} apps intact"
else
  data="data: $(grep -m1 '^  FAIL' <<<"$data_out" | sed 's/^  FAIL  //' || echo 'check failed')"
fi

say "${e2e}; ${data}"
[[ $e2e_ok -eq 1 && $data_exit -eq 0 ]]
